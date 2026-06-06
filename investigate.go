package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
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

// --- Google Health API v4 ingestion ---
//
// POST /api/ingest/health accepts a {"dataPoints": [ {dataSource, exercise}, ...]}
// body and maps each exercise onto the runs table, reusing the Strava upsert path.
//
// IMPORTANT: fetch from the `:reconcile` endpoint, NOT plain `dataPoints` (list).
// `list` returns raw per-source data points with no deletion flag, so it includes
// cross-source duplicates and entries the user deleted in the Google Health app
// (e.g. a Strava-recorded copy of a run that also exists from Fitbit). `reconcile`
// returns the deduplicated canonical stream the Health app itself shows. The only
// shape difference: reconcile names the id "dataPointName"; list names it "name" —
// both are handled below.
//
// NOTE on real-world data: Health Connect exercises synced from Strava/Fitbit
// are aggregate-only — no per-split summaries, cadence, HR, or GPS. The structs
// below also model the documented per-mobility/HR aggregate fields so the mapper
// fills them when present, but in practice only distance/pace/duration/elevation
// arrive populated. There is no route geometry in this data type at all (GPS
// would require a separate TCX export — see TODO.md).

// healthListResp is the Health API exercise dataPoints list response.
type healthListResp struct {
	DataPoints []healthDataPoint `json:"dataPoints"`
}

type healthDataPoint struct {
	// list returns the id as "name"; reconcile returns it as "dataPointName".
	Name          string `json:"name"`
	DataPointName string `json:"dataPointName"`
	DataSource    struct {
		Device struct {
			Manufacturer string `json:"manufacturer"`
		} `json:"device"`
		Application struct {
			PackageName string `json:"packageName"`
		} `json:"application"`
		Platform string `json:"platform"`
	} `json:"dataSource"`
	Exercise healthExercise `json:"exercise"`
}

type healthExercise struct {
	Interval struct {
		StartTime      string `json:"startTime"`
		StartUtcOffset string `json:"startUtcOffset"`
		EndTime        string `json:"endTime"`
		EndUtcOffset   string `json:"endUtcOffset"`
	} `json:"interval"`
	ExerciseType   string        `json:"exerciseType"`
	MetricsSummary healthMetrics `json:"metricsSummary"`
	DisplayName    string        `json:"displayName"`
	ActiveDuration string        `json:"activeDuration"`
	Notes          string        `json:"notes"`
}

type healthMetrics struct {
	CaloriesKcal                     float64 `json:"caloriesKcal"`
	DistanceMillimeters              float64 `json:"distanceMillimeters"`
	Steps                            string  `json:"steps"`
	AverageSpeedMillimetersPerSecond float64 `json:"averageSpeedMillimetersPerSecond"`
	AveragePaceSecondsPerMeter       float64 `json:"averagePaceSecondsPerMeter"`
	AverageHeartRateBeatsPerMinute   string  `json:"averageHeartRateBeatsPerMinute"`
	ElevationGainMillimeters         float64 `json:"elevationGainMillimeters"`
	MobilityMetrics                  struct {
		AvgCadenceStepsPerMinute float64 `json:"avgCadenceStepsPerMinute"`
	} `json:"mobilityMetrics"`
}

// healthStravaID derives a stable, positive synthetic strava_id from a Google
// Health data-point id by hashing it, so ingestion stays idempotent (INSERT OR
// REPLACE) for the same source activity.
func healthStravaID(id string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("gh_" + id))
	return int64(h.Sum64() & 0x7fffffffffffffff)
}

// parseHealthDuration parses a protobuf Duration string ("4124s", "4124.5s")
// into whole seconds.
func parseHealthDuration(s string) int {
	s = strings.TrimSuffix(strings.TrimSpace(s), "s")
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int(f + 0.5)
}

func parseFloatStr(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// applyOffset returns wall-clock local time as RFC3339 with a Z suffix (the
// app's start_date_local convention) for a UTC timestamp plus an offset.
func applyOffset(utc string, offSec int) string {
	t, err := time.Parse(time.RFC3339, utc)
	if err != nil {
		return utc
	}
	return t.Add(time.Duration(offSec) * time.Second).UTC().Format("2006-01-02T15:04:05Z")
}

// formatTZ renders an offset (seconds) as a display string like "(GMT+02:00)".
func formatTZ(offSec int) string {
	sign := "+"
	if offSec < 0 {
		sign = "-"
		offSec = -offSec
	}
	return fmt.Sprintf("(GMT%s%02d:%02d)", sign, offSec/3600, (offSec%3600)/60)
}

// mapExerciseType maps a Health ExerciseType enum to a display type.
func mapExerciseType(t string) string {
	switch t {
	case "", "RUNNING", "RUNNING_TREADMILL":
		return "Run"
	case "WALKING":
		return "Walk"
	default:
		parts := strings.Split(strings.ToLower(t), "_")
		for i, p := range parts {
			if p != "" {
				parts[i] = strings.ToUpper(p[:1]) + p[1:]
			}
		}
		return strings.Join(parts, " ")
	}
}

// healthDeviceName derives a human label from a data point's provenance.
func healthDeviceName(dp *healthDataPoint) string {
	if dp.DataSource.Application.PackageName == "com.strava" {
		return "Strava (Health Connect)"
	}
	switch dp.DataSource.Platform {
	case "FITBIT":
		return "Fitbit"
	case "HEALTH_CONNECT":
		return "Health Connect"
	}
	if dp.DataSource.Device.Manufacturer != "" {
		return dp.DataSource.Device.Manufacturer
	}
	return "Google Health"
}

func healthIDFromName(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// sameMagnitude reports whether a and b are within tol (a fraction) of the larger.
func sameMagnitude(a, b, tol float64) bool {
	hi := math.Max(a, b)
	if hi == 0 {
		return true
	}
	return math.Abs(a-b)/hi <= tol
}

// findDuplicateRunID returns the id of an existing run that is the same activity
// as the given one, ignoring rows with excludeStravaID (the data point's own row,
// so re-ingestion is a plain upsert rather than a self-merge).
//
// Two records are the same activity when EITHER their time intervals overlap (you
// can't run two activities at once), OR they fall on the same calendar day with
// near-identical distance and duration. The second rule catches one run synced
// from different sources with mismatched start times — e.g. a manual Fitbit entry
// whose timestamp is shifted from the Strava-recorded run.
//
// Candidates are narrowed to the same calendar day in SQL, then checked in Go
// (start_date strings carry a Z suffix SQLite's date functions don't reliably
// parse).
func findDuplicateRunID(db *sql.DB, startUTC string, durationSec int, distanceM float64, excludeStravaID int64) (int64, bool) {
	t0, err := time.Parse(time.RFC3339, startUTC)
	if err != nil || len(startUTC) < 10 {
		return 0, false
	}
	t1 := t0.Add(time.Duration(durationSec) * time.Second)
	rows, err := db.Query(
		`SELECT id, strava_id, start_date, elapsed_time_seconds, moving_time_seconds, distance_meters
		   FROM runs WHERE substr(start_date,1,10) = ?`, startUTC[:10])
	if err != nil {
		return 0, false
	}
	defer rows.Close()
	for rows.Next() {
		var id, sid int64
		var sd string
		var elapsed, moving int
		var dist float64
		if rows.Scan(&id, &sid, &sd, &elapsed, &moving, &dist) != nil {
			continue
		}
		if sid == excludeStravaID {
			continue
		}
		s0, err := time.Parse(time.RFC3339, sd)
		if err != nil {
			continue
		}
		dur := elapsed
		if dur == 0 {
			dur = moving
		}
		s1 := s0.Add(time.Duration(dur) * time.Second)

		// Intervals overlap (half-open) -> same activity.
		if t0.Before(s1) && s0.Before(t1) {
			return id, true
		}
		// Same day + near-identical distance and duration -> same activity
		// recorded by a different source with a shifted timestamp.
		if distanceM > 0 && dist > 0 &&
			sameMagnitude(distanceM, dist, 0.02) &&
			sameMagnitude(float64(durationSec), float64(dur), 0.20) {
			return id, true
		}
	}
	return 0, false
}

// mergeHealthIntoRun enriches an existing run with scalar fields from an
// overlapping Google Health activity, filling ONLY values the existing run is
// missing (so the richer source — Strava splits/GPS — is never overwritten).
func mergeHealthIntoRun(db *sql.DB, id int64, a *stravaActivity) error {
	_, err := db.Exec(`UPDATE runs SET
		calories             = CASE WHEN calories = 0 THEN ? ELSE calories END,
		average_cadence      = CASE WHEN average_cadence = 0 THEN ? ELSE average_cadence END,
		total_elevation_gain = CASE WHEN total_elevation_gain = 0 THEN ? ELSE total_elevation_gain END,
		average_heartrate    = CASE WHEN has_heartrate = 0 THEN ? ELSE average_heartrate END,
		has_heartrate        = CASE WHEN has_heartrate = 0 AND ? = 1 THEN 1 ELSE has_heartrate END,
		updated_at           = datetime('now')
		WHERE id = ?`,
		a.Calories, a.AverageCadence, a.TotalElevationGain,
		a.AverageHeartrate, boolToInt(a.HasHeartrate), id)
	return err
}

// upsertHealthDataPoint maps one Health exercise data point onto the runs table,
// reusing the Strava upsert path so the column mapping stays in one place.
// Returns merged=true when it enriched an existing overlapping run instead of
// inserting a new one.
func upsertHealthDataPoint(db *sql.DB, dp *healthDataPoint) (merged bool, err error) {
	ex := dp.Exercise
	m := ex.MetricsSummary

	distM := m.DistanceMillimeters / 1000.0 // mm -> m
	moving := parseHealthDuration(ex.ActiveDuration)
	offSec := parseHealthDuration(ex.Interval.StartUtcOffset)

	// Elapsed time from the interval when both ends parse, else moving time.
	elapsed := moving
	if st, err1 := time.Parse(time.RFC3339, ex.Interval.StartTime); err1 == nil {
		if en, err2 := time.Parse(time.RFC3339, ex.Interval.EndTime); err2 == nil {
			if d := int(en.Sub(st).Seconds()); d > 0 {
				elapsed = d
			}
		}
	}

	// Average speed (m/s): prefer explicit, then pace, then distance/time.
	avgSpeed := m.AverageSpeedMillimetersPerSecond / 1000.0
	if avgSpeed == 0 && m.AveragePaceSecondsPerMeter > 0 {
		avgSpeed = 1.0 / m.AveragePaceSecondsPerMeter
	}
	if avgSpeed == 0 && moving > 0 {
		avgSpeed = distM / float64(moving)
	}

	hr := parseFloatStr(m.AverageHeartRateBeatsPerMinute)

	// Health reports cadence as steps/min; average_cadence stores per-leg rpm
	// (store.go doubles it for the stats page), so halve to keep that contract.
	cadence := m.MobilityMetrics.AvgCadenceStepsPerMinute / 2.0

	a := stravaActivity{
		ID:                 healthStravaID(healthIDFromName(firstNonEmpty(dp.Name, dp.DataPointName))),
		Name:               firstNonEmpty(ex.DisplayName, mapExerciseType(ex.ExerciseType)),
		Description:        ex.Notes,
		Type:               mapExerciseType(ex.ExerciseType),
		SportType:          mapExerciseType(ex.ExerciseType),
		Timezone:           formatTZ(offSec),
		StartDate:          ex.Interval.StartTime,
		StartDateLocal:     applyOffset(ex.Interval.StartTime, offSec),
		Distance:           distM,
		MovingTime:         moving,
		ElapsedTime:        elapsed,
		AverageSpeed:       avgSpeed,
		AverageCadence:     cadence,
		HasHeartrate:       hr > 0,
		AverageHeartrate:   hr,
		TotalElevationGain: m.ElevationGainMillimeters / 1000.0,
		Calories:           int(m.CaloriesKcal + 0.5),
		DeviceName:         healthDeviceName(dp),
	}

	// Merge into an existing run that overlaps in time (same activity from
	// another source, e.g. the Strava export) rather than creating a duplicate.
	if existingID, ok := findDuplicateRunID(db, ex.Interval.StartTime, elapsed, distM, a.ID); ok {
		return true, mergeHealthIntoRun(db, existingID, &a)
	}
	return false, upsertActivity(db, &a)
}

// POST /api/ingest/health
//
// Accepts the raw Google Health v4 exercise dataPoints response
// ({"dataPoints":[...]}) and upserts each exercise into the runs table. A
// synthetic strava_id is derived from each data-point id for idempotency.
func (a *App) handleIngestHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req healthListResp
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if len(req.DataPoints) == 0 {
		writeErr(w, http.StatusBadRequest, "\"dataPoints\" array is empty or missing")
		return
	}

	imported, merged, errCount, skipped := 0, 0, 0, 0
	names := []string{}
	for i := range req.DataPoints {
		dp := &req.DataPoints[i]
		if dp.Exercise.Interval.StartTime == "" {
			skipped++
			continue
		}
		wasMerged, err := upsertHealthDataPoint(a.db, dp)
		if err != nil {
			errCount++
			continue
		}
		if wasMerged {
			merged++
		} else {
			imported++
			names = append(names, firstNonEmpty(dp.Exercise.DisplayName, "Run"))
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"imported": imported,
		"merged":   merged,
		"errors":   errCount,
		"skipped":  skipped,
		"runs":     names,
	})
}
