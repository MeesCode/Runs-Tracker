package main

import (
	"database/sql"
	"encoding/json"
	"hash/fnv"
	"net/http"
)

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

// stravaIngestReq is the request body for POST /api/ingest/strava. Each entry in
// Runs is a raw Strava activity object (same shape as strava-export.json).
type stravaIngestReq struct {
	Runs []stravaActivity `json:"runs"`
}

// POST /api/ingest/strava
//
// Accepts a JSON body of Strava activities and upserts each into the runs table
// (INSERT OR REPLACE keyed on strava_id), using the same mapping as the seed.
func (a *App) handleIngestStrava(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req stravaIngestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if len(req.Runs) == 0 {
		writeErr(w, http.StatusBadRequest, "\"runs\" array is empty or missing")
		return
	}

	imported, errCount := 0, 0
	names := []string{}
	for i := range req.Runs {
		if err := upsertActivity(a.db, &req.Runs[i]); err != nil {
			errCount++
			continue
		}
		imported++
		names = append(names, req.Runs[i].Name)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"imported": imported,
		"errors":   errCount,
		"runs":     names,
	})
}

// healthActivity is the best-guess Google Health API v4 activity shape.
type healthActivity struct {
	ID                          string  `json:"id"`
	Name                        string  `json:"name"`
	Description                 string  `json:"description"`
	ActivityType                string  `json:"activityType"`
	StartTime                   string  `json:"startTime"`
	EndTime                     string  `json:"endTime"`
	DistanceMeters              float64 `json:"distanceMeters"`
	MovingTimeSeconds           int     `json:"movingTimeSeconds"`
	AverageSpeedMetersPerSecond float64 `json:"averageSpeedMetersPerSecond"`
	MaxSpeedMetersPerSecond     float64 `json:"maxSpeedMetersPerSecond"`
	AverageHeartRate            float64 `json:"averageHeartRate"`
	MaxHeartRate                float64 `json:"maxHeartRate"`
	AverageCadence              float64 `json:"averageCadence"`
	ElevationGainMeters         float64 `json:"elevationGainMeters"`
	Calories                    int     `json:"calories"`
	RoutePolyline               string  `json:"routePolyline"`
	StartLatitude               float64 `json:"startLatitude"`
	StartLongitude              float64 `json:"startLongitude"`
	EndLatitude                 float64 `json:"endLatitude"`
	EndLongitude                float64 `json:"endLongitude"`
}

// healthIngestReq is the request body for POST /api/ingest/health.
type healthIngestReq struct {
	Activities []healthActivity `json:"activities"`
}

// healthStravaID derives a stable, positive synthetic strava_id from a Google
// Health activity id by hashing it, so ingestion stays idempotent (INSERT OR
// REPLACE) for the same source activity.
func healthStravaID(id string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("gh_" + id))
	return int64(h.Sum64() & 0x7fffffffffffffff)
}

// upsertHealthActivity maps a Google Health activity onto the runs table,
// reusing the Strava upsert path so the column mapping stays in one place.
func upsertHealthActivity(db *sql.DB, h *healthActivity) error {
	a := stravaActivity{
		ID:                 healthStravaID(h.ID),
		Name:               h.Name,
		Description:        h.Description,
		Type:               "Run",
		SportType:          "Run",
		Timezone:           "Europe/Amsterdam",
		StartDate:          h.StartTime,
		StartDateLocal:     h.StartTime,
		Distance:           h.DistanceMeters,
		MovingTime:         h.MovingTimeSeconds,
		ElapsedTime:        h.MovingTimeSeconds,
		AverageSpeed:       h.AverageSpeedMetersPerSecond,
		MaxSpeed:           h.MaxSpeedMetersPerSecond,
		AverageCadence:     h.AverageCadence,
		HasHeartrate:       h.AverageHeartRate > 0,
		AverageHeartrate:   h.AverageHeartRate,
		MaxHeartrate:       h.MaxHeartRate,
		TotalElevationGain: h.ElevationGainMeters,
		Calories:           h.Calories,
		DeviceName:         "Google Health",
	}
	a.Map.SummaryPolyline = h.RoutePolyline
	if h.StartLatitude != 0 || h.StartLongitude != 0 {
		a.StartLatLng = []float64{h.StartLatitude, h.StartLongitude}
	}
	if h.EndLatitude != 0 || h.EndLongitude != 0 {
		a.EndLatLng = []float64{h.EndLatitude, h.EndLongitude}
	}
	return upsertActivity(db, &a)
}

// POST /api/ingest/health
//
// Accepts a JSON body of Google Health activities and upserts each into the runs
// table. A synthetic strava_id is derived from the Google Health activity id.
func (a *App) handleIngestHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req healthIngestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if len(req.Activities) == 0 {
		writeErr(w, http.StatusBadRequest, "\"activities\" array is empty or missing")
		return
	}

	imported, errCount := 0, 0
	names := []string{}
	for i := range req.Activities {
		if err := upsertHealthActivity(a.db, &req.Activities[i]); err != nil {
			errCount++
			continue
		}
		imported++
		names = append(names, req.Activities[i].Name)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"imported":   imported,
		"errors":     errCount,
		"activities": names,
	})
}
