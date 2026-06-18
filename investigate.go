package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
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
		a.Map.SummaryPolyline, a.DeviceName, a.KudosCount, a.Description, int(a.Calories),
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

	before := countRuns(a.db)
	errCount := 0
	names := []string{}
	hrFetched := 0
	stravaReady := a.strava != nil && a.strava.configured()
	for i := range req.Runs {
		run := &req.Runs[i]
		if err := upsertActivity(a.db, run); err != nil {
			errCount++
			continue
		}
		names = append(names, run.Name)
		// Pull the real per-second HR stream for new activities so the detail
		// view shows actual data instead of the synthetic wobble. Best effort:
		// failures don't fail the ingest.
		if stravaReady && run.HasHeartrate && run.ID != 0 {
			if i > 0 {
				time.Sleep(stravaRateDelay)
			}
			if ok, err := a.storeHRStream(run.ID); err != nil {
				log.Printf("ingest: HR stream fetch for %d failed: %v", run.ID, err)
			} else if ok {
				hrFetched++
			}
		}
	}
	merged, _ := dedupeRuns(a.db)
	imported := countRuns(a.db) - before
	if imported < 0 {
		imported = 0
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"imported":   imported,
		"merged":     merged,
		"errors":     errCount,
		"runs":       names,
		"hr_fetched": hrFetched,
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

func countRuns(db *sql.DB) int {
	var n int
	_ = db.QueryRow("SELECT COUNT(*) FROM runs").Scan(&n)
	return n
}

// dedupRow is a minimal projection of a run used for duplicate detection.
type dedupRow struct {
	id    int64
	start time.Time
	dur   int
	dist  float64
	cal   int
	cad   float64
	hr    float64
	hasHR bool
	elev  float64
	rich  bool // has real Strava data: GPS polyline / splits / best efforts
	valid bool // start_date parsed
}

// loadDedupRows projects every run for duplicate analysis.
func loadDedupRows(db *sql.DB) ([]dedupRow, error) {
	rows, err := db.Query(`SELECT id, start_date, elapsed_time_seconds, moving_time_seconds,
		distance_meters, calories, average_cadence, average_heartrate, has_heartrate,
		total_elevation_gain, COALESCE(summary_polyline,''), COALESCE(splits_metric,''),
		COALESCE(best_efforts,'')
		FROM runs ORDER BY start_date, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dedupRow
	for rows.Next() {
		var r dedupRow
		var sd, poly, splits, best string
		var elapsed, moving, hasHR int
		if err := rows.Scan(&r.id, &sd, &elapsed, &moving, &r.dist, &r.cal, &r.cad,
			&r.hr, &hasHR, &r.elev, &poly, &splits, &best); err != nil {
			continue
		}
		r.dur = elapsed
		if r.dur == 0 {
			r.dur = moving
		}
		r.hasHR = hasHR != 0
		r.rich = poly != "" || splits != "" || best != ""
		if t, e := time.Parse(time.RFC3339, sd); e == nil {
			r.start = t
			r.valid = true
		}
		out = append(out, r)
	}
	return out, nil
}

// runsDuplicate reports whether two runs are the same activity: their intervals
// overlap, or — for cross-source records (at least one lacking rich Strava data)
// — they share a calendar day with near-identical distance and duration. The
// fuzzy rule is gated on cross-source so two distinct same-distance Strava runs
// on one day are never collapsed.
func runsDuplicate(a, b *dedupRow) bool {
	if !a.valid || !b.valid {
		return false
	}
	if a.start.Year() != b.start.Year() || a.start.YearDay() != b.start.YearDay() {
		return false
	}
	aEnd := a.start.Add(time.Duration(a.dur) * time.Second)
	bEnd := b.start.Add(time.Duration(b.dur) * time.Second)
	if a.start.Before(bEnd) && b.start.Before(aEnd) {
		return true // intervals overlap
	}
	if (!a.rich || !b.rich) && a.dist > 0 && b.dist > 0 &&
		sameMagnitude(a.dist, b.dist, 0.02) &&
		sameMagnitude(float64(a.dur), float64(b.dur), 0.20) {
		return true
	}
	return false
}

// betterSurvivor reports whether a should survive a merge over b: prefer the
// richer record (real Strava data is authoritative — its start/stop times take
// precedence over Google Health), else the lower id.
func betterSurvivor(a, b *dedupRow) bool {
	if a.rich != b.rich {
		return a.rich
	}
	return a.id < b.id
}

// dedupeRuns collapses runs that are the same activity into a single row. The
// surviving row (richer / Strava-authoritative, keeping its own start/stop times)
// is backfilled with any scalar fields it's missing (calories, cadence, HR,
// elevation) from the rows removed. Idempotent; safe to run after every ingest.
// Returns the number of rows removed.
func dedupeRuns(db *sql.DB) (int, error) {
	all, err := loadDedupRows(db)
	if err != nil {
		return 0, err
	}
	n := len(all)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for i := 0; i < n; i++ {
		for j := 0; j < i; j++ {
			if runsDuplicate(&all[i], &all[j]) {
				parent[find(i)] = find(j)
			}
		}
	}
	groups := map[int][]int{}
	for i := 0; i < n; i++ {
		root := find(i)
		groups[root] = append(groups[root], i)
	}

	var removed []int64
	for _, idxs := range groups {
		if len(idxs) < 2 {
			continue
		}
		s := idxs[0]
		for _, k := range idxs[1:] {
			if betterSurvivor(&all[k], &all[s]) {
				s = k
			}
		}
		surv := &all[s]
		changed := false
		for _, k := range idxs {
			if k == s {
				continue
			}
			lo := &all[k]
			if surv.cal == 0 && lo.cal != 0 {
				surv.cal = lo.cal
				changed = true
			}
			if surv.cad == 0 && lo.cad != 0 {
				surv.cad = lo.cad
				changed = true
			}
			if surv.elev == 0 && lo.elev != 0 {
				surv.elev = lo.elev
				changed = true
			}
			if !surv.hasHR && lo.hasHR {
				surv.hasHR = true
				surv.hr = lo.hr
				changed = true
			}
			removed = append(removed, lo.id)
		}
		if changed {
			if _, err := db.Exec(`UPDATE runs SET calories=?, average_cadence=?,
				average_heartrate=?, has_heartrate=?, total_elevation_gain=?, updated_at=datetime('now')
				WHERE id=?`, surv.cal, surv.cad, surv.hr, boolToInt(surv.hasHR), surv.elev, surv.id); err != nil {
				return len(removed), err
			}
		}
	}
	for _, id := range removed {
		if _, err := db.Exec(`DELETE FROM runs WHERE id = ?`, id); err != nil {
			return len(removed), err
		}
	}
	return len(removed), nil
}

// upsertHealthDataPoint maps one Health exercise data point onto the runs table,
// reusing the Strava upsert path. Cross-source duplicates are collapsed afterward
// by dedupeRuns (callers run it once per batch).
func upsertHealthDataPoint(db *sql.DB, dp *healthDataPoint) error {
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
		Calories:           m.CaloriesKcal,
		DeviceName:         healthDeviceName(dp),
	}

	return upsertActivity(db, &a)
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

	before := countRuns(a.db)
	skipped := 0
	for i := range req.DataPoints {
		dp := &req.DataPoints[i]
		if dp.Exercise.Interval.StartTime == "" || upsertHealthDataPoint(a.db, dp) != nil {
			skipped++
			continue
		}
	}
	merged, _ := dedupeRuns(a.db)
	imported := countRuns(a.db) - before
	if imported < 0 {
		imported = 0
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"imported": imported,
		"merged":   merged,
		"skipped":  skipped,
	})
}
