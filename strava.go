package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	stravaTokenURL  = "https://www.strava.com/api/v3/oauth/token"
	stravaStreamURL = "https://www.strava.com/api/v3/activities/%d/streams?keys=heartrate,time,distance&key_by_type=true"

	// defaultStravaCredsFile is where the host stores the long-lived Strava
	// OAuth credentials. Overridable via STRAVA_CREDENTIALS_FILE.
	defaultStravaCredsFile = "/root/.strava_credentials.json"

	// stravaRateDelay keeps us well under Strava's 100 requests / 15 min limit
	// (a request every 0.6s caps at ~150/15min worst case, but the backfill is
	// the only heavy caller and runs serially).
	stravaRateDelay = 700 * time.Millisecond
)

// stravaCredentials mirrors the on-disk credentials file. Numeric client_id is
// read leniently (the file stores it as a JSON number) and all fields are
// round-tripped on write so a token refresh doesn't drop the others.
type stravaCredentials struct {
	ClientID       json.Number `json:"client_id,omitempty"`
	ClientSecret   string      `json:"client_secret,omitempty"`
	AccessToken    string      `json:"access_token,omitempty"`
	RefreshToken   string      `json:"refresh_token,omitempty"`
	AthleteID      int64       `json:"athlete_id,omitempty"`
	ExpiresAt      int64       `json:"expires_at,omitempty"`
	Scope          string      `json:"scope,omitempty"`
	WebhookSecret  string      `json:"webhook_secret,omitempty"`
	TokenExpiresAt int64       `json:"token_expires_at,omitempty"`
}

// stravaAuth manages a cached Strava access token, refreshing it via the
// refresh-token grant when it nears expiry and persisting the new token back to
// the credentials file when that file is writable.
type stravaAuth struct {
	credsFile string

	mu    sync.Mutex
	creds stravaCredentials
}

// newStravaAuth loads credentials from the file at STRAVA_CREDENTIALS_FILE
// (default /root/.strava_credentials.json), falling back to the
// STRAVA_CLIENT_ID / STRAVA_CLIENT_SECRET / STRAVA_REFRESH_TOKEN / etc.
// environment variables when the file is absent or unreadable.
func newStravaAuth() *stravaAuth {
	path := getenv("STRAVA_CREDENTIALS_FILE", defaultStravaCredsFile)
	a := &stravaAuth{credsFile: path}

	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &a.creds); err != nil {
			log.Printf("strava: credentials file %s is invalid JSON: %v", path, err)
		}
	} else {
		log.Printf("strava: credentials file %s not readable (%v); falling back to env", path, err)
	}

	// Environment variables override / fill in any missing fields.
	if v := os.Getenv("STRAVA_CLIENT_ID"); v != "" {
		a.creds.ClientID = json.Number(v)
	}
	if v := os.Getenv("STRAVA_CLIENT_SECRET"); v != "" {
		a.creds.ClientSecret = v
	}
	if v := os.Getenv("STRAVA_REFRESH_TOKEN"); v != "" {
		a.creds.RefreshToken = v
	}
	if v := os.Getenv("STRAVA_ACCESS_TOKEN"); v != "" {
		a.creds.AccessToken = v
	}
	if v := os.Getenv("STRAVA_EXPIRES_AT"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			a.creds.ExpiresAt = n
		}
	}
	return a
}

// configured reports whether enough credentials exist to attempt a refresh.
func (a *stravaAuth) configured() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.creds.ClientID.String() != "" && a.creds.ClientSecret != "" && a.creds.RefreshToken != ""
}

// accessToken returns a valid access token, refreshing via the refresh-token
// grant when the cached one is missing or within 60s of expiry.
func (a *stravaAuth) accessToken() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.creds.AccessToken != "" && a.creds.ExpiresAt > time.Now().Add(60*time.Second).Unix() {
		return a.creds.AccessToken, nil
	}
	if a.creds.ClientID.String() == "" || a.creds.ClientSecret == "" || a.creds.RefreshToken == "" {
		return "", fmt.Errorf("strava: not configured (need client_id, client_secret, refresh_token)")
	}

	resp, err := http.PostForm(stravaTokenURL, url.Values{
		"client_id":     {a.creds.ClientID.String()},
		"client_secret": {a.creds.ClientSecret},
		"refresh_token": {a.creds.RefreshToken},
		"grant_type":    {"refresh_token"},
	})
	if err != nil {
		return "", fmt.Errorf("strava token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("strava token refresh failed (%d): %s", resp.StatusCode, truncate(string(body), 200))
	}

	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    int64  `json:"expires_at"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("strava token parse: %w", err)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("strava: no access_token in refresh response")
	}

	a.creds.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		a.creds.RefreshToken = tr.RefreshToken
	}
	if tr.ExpiresAt > 0 {
		a.creds.ExpiresAt = tr.ExpiresAt
	} else if tr.ExpiresIn > 0 {
		a.creds.ExpiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second).Unix()
	}
	a.creds.TokenExpiresAt = a.creds.ExpiresAt
	a.persistLocked()
	return a.creds.AccessToken, nil
}

// persistLocked writes the credentials back to disk (best effort). Callers hold
// a.mu. A failure (e.g. read-only mount) is logged but not fatal — the new
// token still lives in memory for this process's lifetime.
func (a *stravaAuth) persistLocked() {
	if a.credsFile == "" {
		return
	}
	data, err := json.MarshalIndent(a.creds, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(a.credsFile, data, 0o600); err != nil {
		log.Printf("strava: could not persist refreshed token to %s: %v", a.credsFile, err)
	}
}

// fetchHRStream pulls the heartrate/time/distance streams for one activity and
// returns the compact storedHRStream JSON (heartrate + paired distance arrays).
// Returns ("", nil) when the activity has no HR stream, so the caller can mark
// it processed without storing anything.
func (a *stravaAuth) fetchHRStream(stravaID int64) (string, error) {
	token, err := a.accessToken()
	if err != nil {
		return "", err
	}

	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf(stravaStreamURL, stravaID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("strava streams request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusNotFound {
		return "", nil // no streams for this activity
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("strava streams: rate limited (429)")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("strava streams failed (%d): %s", resp.StatusCode, truncate(string(body), 200))
	}

	// key_by_type=true response: { "heartrate": {"data":[...]}, "distance": {"data":[...]}, ... }
	var keyed struct {
		Heartrate struct {
			Data []float64 `json:"data"`
		} `json:"heartrate"`
		Distance struct {
			Data []float64 `json:"data"`
		} `json:"distance"`
	}
	if err := json.Unmarshal(body, &keyed); err != nil {
		return "", fmt.Errorf("strava streams parse: %w", err)
	}
	if len(keyed.Heartrate.Data) == 0 {
		return "", nil // no usable HR data
	}

	out, err := json.Marshal(storedHRStream{
		Heartrate: keyed.Heartrate.Data,
		Distance:  keyed.Distance.Data,
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// storeHRStream fetches and persists the HR stream for one activity (keyed on
// strava_id). Reports whether a stream was actually stored.
func (a *App) storeHRStream(stravaID int64) (bool, error) {
	if a.strava == nil || !a.strava.configured() {
		return false, fmt.Errorf("strava not configured")
	}
	raw, err := a.strava.fetchHRStream(stravaID)
	if err != nil {
		return false, err
	}
	if raw == "" {
		return false, nil
	}
	if _, err := a.db.Exec(`UPDATE runs SET hr_stream = ?, updated_at = datetime('now') WHERE strava_id = ?`,
		raw, stravaID); err != nil {
		return false, err
	}
	return true, nil
}

// backfillResult summarizes one HR-stream backfill run.
type backfillResult struct {
	Candidates int      `json:"candidates"` // runs missing an HR stream
	Fetched    int      `json:"fetched"`    // streams stored
	Empty      int      `json:"empty"`      // activities Strava had no HR stream for
	Errors     int      `json:"errors"`     // failed fetches
	Messages   []string `json:"messages,omitempty"`
}

// backfillHRStreams fetches and stores HR streams for every run with
// has_heartrate = 1 AND hr_stream IS NULL, rate-limited to respect Strava's API
// quota. Serialized via hrMu so concurrent callers can't double-fetch.
func (a *App) backfillHRStreams() (backfillResult, error) {
	a.hrMu.Lock()
	defer a.hrMu.Unlock()

	var res backfillResult
	if a.strava == nil || !a.strava.configured() {
		return res, fmt.Errorf("strava not configured (need client_id, client_secret, refresh_token)")
	}

	rows, err := a.db.Query(`SELECT strava_id FROM runs
		WHERE has_heartrate = 1 AND hr_stream IS NULL AND strava_id IS NOT NULL
		ORDER BY start_date DESC`)
	if err != nil {
		return res, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	res.Candidates = len(ids)

	for i, id := range ids {
		if i > 0 {
			time.Sleep(stravaRateDelay)
		}
		stored, err := a.storeHRStream(id)
		switch {
		case err != nil:
			res.Errors++
			res.Messages = append(res.Messages, fmt.Sprintf("%d: %v", id, err))
			// A 429 means we've hit the quota; stop early rather than burn more.
			if isRateLimited(err) {
				res.Messages = append(res.Messages, "stopped early: rate limited by Strava")
				return res, nil
			}
		case stored:
			res.Fetched++
		default:
			res.Empty++
			// Synthetic Google-Health strava_ids have no real Strava activity;
			// mark them processed with an empty blob so they aren't retried.
			_, _ = a.db.Exec(`UPDATE runs SET hr_stream = '{"heartrate":[],"distance":[]}' WHERE strava_id = ?`, id)
		}
	}
	return res, nil
}

func isRateLimited(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate limited"))
}

// POST /api/backfill/hr — fetch real HR streams from Strava for all runs that
// don't have one yet. Rate-limited and serialized.
func (a *App) handleBackfillHR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.strava == nil || !a.strava.configured() {
		writeErr(w, http.StatusServiceUnavailable,
			"Strava not configured (set STRAVA_CREDENTIALS_FILE or STRAVA_CLIENT_ID/SECRET/REFRESH_TOKEN)")
		return
	}
	res, err := a.backfillHRStreams()
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
