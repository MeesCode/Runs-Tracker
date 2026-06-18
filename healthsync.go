package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const healthReconcileURL = "https://health.googleapis.com/v4/users/me/dataTypes/exercise/dataPoints:reconcile"

// googleAuth manages a cached Google OAuth access token, refreshing it from the
// refresh token in the environment when it nears expiry. All fields come from
// GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET / GOOGLE_REFRESH_TOKEN / GOOGLE_TOKEN_URI.
type googleAuth struct {
	clientID     string
	clientSecret string
	refreshToken string
	tokenURI     string

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func newGoogleAuth() *googleAuth {
	return &googleAuth{
		clientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		clientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		refreshToken: os.Getenv("GOOGLE_REFRESH_TOKEN"),
		tokenURI:     getenv("GOOGLE_TOKEN_URI", "https://oauth2.googleapis.com/token"),
	}
}

func (g *googleAuth) configured() bool {
	return g.clientID != "" && g.clientSecret != "" && g.refreshToken != ""
}

// accessToken returns a valid access token, refreshing via the refresh-token
// grant when the cached one is missing or within 60s of expiry.
func (g *googleAuth) accessToken() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token != "" && time.Now().Before(g.expiresAt.Add(-60*time.Second)) {
		return g.token, nil
	}

	resp, err := http.PostForm(g.tokenURI, url.Values{
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"refresh_token": {g.refreshToken},
		"grant_type":    {"refresh_token"},
	})
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		if strings.Contains(string(body), "invalid_grant") {
			return "", fmt.Errorf("refresh token rejected (invalid_grant) — re-run scripts/google_auth.py. " +
				"If the OAuth app is in Testing mode, refresh tokens expire after 7 days; publish it to production to avoid this")
		}
		return "", fmt.Errorf("token refresh failed (%d): %s", resp.StatusCode, truncate(string(body), 200))
	}

	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("token parse: %w", err)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("no access_token in refresh response")
	}
	g.token = tr.AccessToken
	ttl := tr.ExpiresIn
	if ttl <= 0 {
		ttl = 3600
	}
	g.expiresAt = time.Now().Add(time.Duration(ttl) * time.Second)
	return g.token, nil
}

// fetchReconcileDataPoints pulls the full reconciled exercise stream, following
// pagination (exercise pageSize maxes at 25). reconcile is the deduplicated,
// canonical stream the Google Health app shows — see investigate.go.
func (g *googleAuth) fetchReconcileDataPoints() ([]healthDataPoint, error) {
	token, err := g.accessToken()
	if err != nil {
		return nil, err
	}
	var all []healthDataPoint
	pageToken := ""
	for {
		u := healthReconcileURL
		if pageToken != "" {
			u += "?pageToken=" + url.QueryEscape(pageToken)
		}
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("reconcile request: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("reconcile failed (%d): %s", resp.StatusCode, truncate(string(body), 300))
		}
		var page struct {
			DataPoints    []healthDataPoint `json:"dataPoints"`
			NextPageToken string            `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("reconcile parse: %w", err)
		}
		all = append(all, page.DataPoints...)
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	return all, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// syncResult summarizes one health sync.
type syncResult struct {
	Imported int `json:"imported"` // net-new runs added
	Merged   int `json:"merged"`   // duplicate rows collapsed by dedupe
	Skipped  int `json:"skipped"`  // malformed / errored points
}

// syncHealth pulls the reconciled exercise stream, upserts each exercise, then
// collapses cross-source duplicates. Serialized so the manual endpoint and the
// background ticker can't race.
func (a *App) syncHealth() (syncResult, error) {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()

	var res syncResult
	dps, err := a.auth.fetchReconcileDataPoints()
	if err != nil {
		return res, err
	}
	before := countRuns(a.db)
	for i := range dps {
		dp := &dps[i]
		if dp.Exercise.Interval.StartTime == "" || upsertHealthDataPoint(a.db, dp) != nil {
			res.Skipped++
			continue
		}
	}
	res.Merged, _ = dedupeRuns(a.db)
	if res.Imported = countRuns(a.db) - before; res.Imported < 0 {
		res.Imported = 0
	}
	return res, nil
}

// POST /api/sync/health — pull from the Google Health API and ingest on demand.
func (a *App) handleSyncHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.auth == nil || !a.auth.configured() {
		writeErr(w, http.StatusServiceUnavailable,
			"Google Health not configured (set GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, GOOGLE_REFRESH_TOKEN)")
		return
	}
	res, err := a.syncHealth()
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// startHealthSyncLoop runs one sync shortly after boot, then every interval.
// No-op when Google Health credentials are absent.
func (a *App) startHealthSyncLoop(interval time.Duration) {
	if a.auth == nil || !a.auth.configured() {
		log.Printf("health sync: disabled (no Google credentials)")
		return
	}
	log.Printf("health sync: enabled, interval=%s", interval)
	go func() {
		time.Sleep(10 * time.Second) // let the server come up first
		for {
			res, err := a.syncHealth()
			if err != nil {
				log.Printf("health sync: error: %v", err)
			} else {
				log.Printf("health sync: imported=%d merged=%d skipped=%d",
					res.Imported, res.Merged, res.Skipped)
			}
			time.Sleep(interval)
		}
	}()
}
