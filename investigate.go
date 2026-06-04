package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// stravaCredentialsPath is where the OAuth credentials live on the host/container.
const stravaCredentialsPath = "/root/.strava_credentials.json"

const (
	stravaActivityURL = "https://www.strava.com/api/v3/activities/%d"
	stravaListURL     = "https://www.strava.com/api/v3/athlete/activities?per_page=200&page=%d"
)

// stravaCredentials mirrors the standard Strava OAuth token file.
type stravaCredentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"` // unix seconds
	TokenType    string `json:"token_type"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// expired reports whether the access token is past its expiry. A zero ExpiresAt
// is treated as "unknown / not expired" so manually-provided tokens still work.
func (c *stravaCredentials) expired(now time.Time) bool {
	return c.ExpiresAt > 0 && now.Unix() >= c.ExpiresAt
}

// loadStravaCredentials reads and parses the credentials file.
func loadStravaCredentials() (*stravaCredentials, error) {
	raw, err := os.ReadFile(stravaCredentialsPath)
	if err != nil {
		return nil, err
	}
	var c stravaCredentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// stravaInvestigateReq is the request body for POST /api/investigate/strava.
type stravaInvestigateReq struct {
	RunIDs []int64 `json:"run_ids"`
	All    bool    `json:"all"`
}

// stravaGet performs an authenticated GET against the Strava API. The returned
// transport error is non-nil only for network-level failures (DNS, connection,
// timeout); HTTP-level problems are reported via the status code.
func stravaGet(token, url string) (body []byte, status int, err error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode, nil
}

// isRun reports whether a Strava activity is a running activity (Run, TrailRun,
// VirtualRun, …) so a full re-sync doesn't pull in rides, swims, etc.
func isRun(a *stravaActivity) bool {
	return a.Type == "Run" || a.SportType == "Run" ||
		a.SportType == "TrailRun" || a.SportType == "VirtualRun"
}

// upsertActivity inserts (or replaces, keyed on the unique strava_id) one Strava
// activity, mapping fields exactly as seedFromExport does.
func upsertActivity(db *sql.DB, a *stravaActivity) error {
	startLat, startLng := latlng(a.StartLatLng)
	endLat, endLng := latlng(a.EndLatLng)

	activityType := a.Type
	if activityType == "" {
		activityType = "Run"
	}
	sportType := a.SportType
	if sportType == "" {
		sportType = "Run"
	}

	const q = `INSERT OR REPLACE INTO runs (
		strava_id, name, activity_type, sport_type,
		start_date_local, start_date, timezone, moving_time_seconds, elapsed_time_seconds,
		distance_meters, average_speed_ms, max_speed_ms,
		has_heartrate, average_heartrate, max_heartrate,
		average_cadence,
		total_elevation_gain, elev_high, elev_low,
		start_lat, start_lng, end_lat, end_lng,
		summary_polyline, device_name, kudos_count, description, calories,
		splits_metric, laps, best_efforts
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

	_, err := db.Exec(q,
		a.ID, a.Name, activityType, sportType,
		a.StartDateLocal, a.StartDate, a.Timezone, a.MovingTime, a.ElapsedTime,
		a.Distance, a.AverageSpeed, a.MaxSpeed,
		boolToInt(a.HasHeartrate), a.AverageHeartrate, a.MaxHeartrate,
		a.AverageCadence,
		a.TotalElevationGain, a.ElevHigh, a.ElevLow,
		startLat, startLng, endLat, endLng,
		a.Map.SummaryPolyline, a.DeviceName, a.KudosCount, a.Description, a.Calories,
		rawOrNull(a.SplitsMetric), rawOrNull(a.Laps), rawOrNull(a.BestEfforts),
	)
	return err
}

// POST /api/investigate/strava
//
// Re-fetches runs from the Strava API and upserts them into the database.
// Body: {"run_ids": [..]} to fetch specific activities, or {"all": true} for a
// full re-sync via the paginated athlete-activities endpoint.
func (a *App) handleInvestigateStrava(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req stravaInvestigateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if !req.All && len(req.RunIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "provide either \"run_ids\" or \"all\": true")
		return
	}

	creds, err := loadStravaCredentials()
	if err != nil {
		if os.IsNotExist(err) || os.IsPermission(err) {
			writeErr(w, http.StatusUnauthorized,
				"strava credentials unavailable at "+stravaCredentialsPath+" ("+err.Error()+"); create/mount the file and refresh the access token")
			return
		}
		writeErr(w, http.StatusInternalServerError, "reading strava credentials: "+err.Error())
		return
	}
	if creds.AccessToken == "" || creds.expired(time.Now()) {
		writeErr(w, http.StatusUnauthorized,
			"strava access token missing or expired; refresh the token in "+stravaCredentialsPath)
		return
	}

	imported, errCount := 0, 0
	names := []string{}

	upsertOne := func(act *stravaActivity) {
		if err := upsertActivity(a.db, act); err != nil {
			errCount++
			return
		}
		imported++
		names = append(names, act.Name)
	}

	if req.All {
		for page := 1; ; page++ {
			body, status, err := stravaGet(creds.AccessToken, fmt.Sprintf(stravaListURL, page))
			if err != nil {
				writeErr(w, http.StatusBadGateway, "strava request failed: "+err.Error())
				return
			}
			if status == http.StatusUnauthorized {
				writeErr(w, http.StatusUnauthorized,
					"strava rejected the access token; refresh the token in "+stravaCredentialsPath)
				return
			}
			if status != http.StatusOK {
				writeErr(w, http.StatusBadGateway, fmt.Sprintf("strava list returned status %d", status))
				return
			}
			var acts []stravaActivity
			if err := json.Unmarshal(body, &acts); err != nil {
				writeErr(w, http.StatusBadGateway, "decoding strava response: "+err.Error())
				return
			}
			if len(acts) == 0 {
				break
			}
			for i := range acts {
				if !isRun(&acts[i]) {
					continue
				}
				upsertOne(&acts[i])
			}
		}
	} else {
		for _, id := range req.RunIDs {
			body, status, err := stravaGet(creds.AccessToken, fmt.Sprintf(stravaActivityURL, id))
			if err != nil {
				writeErr(w, http.StatusBadGateway, "strava request failed: "+err.Error())
				return
			}
			if status == http.StatusUnauthorized {
				writeErr(w, http.StatusUnauthorized,
					"strava rejected the access token; refresh the token in "+stravaCredentialsPath)
				return
			}
			if status != http.StatusOK {
				// e.g. 404 for an unknown id — count it and keep going.
				errCount++
				continue
			}
			var act stravaActivity
			if err := json.Unmarshal(body, &act); err != nil {
				errCount++
				continue
			}
			upsertOne(&act)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"imported": imported,
		"errors":   errCount,
		"runs":     names,
	})
}

// healthInvestigateReq is the request body for POST /api/investigate/health.
type healthInvestigateReq struct {
	ActivityIDs []string `json:"activity_ids"`
	All         bool     `json:"all"`
}

// POST /api/investigate/health
//
// Placeholder for a future Google Health API integration. Validates the request
// shape and always reports that OAuth has not yet been configured.
func (a *App) handleInvestigateHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req healthInvestigateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if !req.All && len(req.ActivityIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "provide either \"activity_ids\" or \"all\": true")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "not_configured",
		"message": "Google Health API OAuth not yet configured. Set up OAuth credentials and a refresh token to use this endpoint.",
		"hint":    "See /home/coder/google-health-discovery.json for the API spec",
	})
}
