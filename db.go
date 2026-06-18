package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

const schema = `
CREATE TABLE IF NOT EXISTS runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    strava_id BIGINT UNIQUE,
    name TEXT NOT NULL DEFAULT '',
    activity_type TEXT DEFAULT 'Run',
    sport_type TEXT DEFAULT 'Run',

    start_date_local TEXT NOT NULL,
    start_date TEXT NOT NULL,
    timezone TEXT DEFAULT 'Europe/Amsterdam',
    moving_time_seconds INTEGER DEFAULT 0,
    elapsed_time_seconds INTEGER DEFAULT 0,

    distance_meters REAL DEFAULT 0,
    average_speed_ms REAL DEFAULT 0,
    max_speed_ms REAL DEFAULT 0,

    has_heartrate INTEGER DEFAULT 0,
    average_heartrate REAL DEFAULT 0,
    max_heartrate REAL DEFAULT 0,

    average_cadence REAL DEFAULT 0,

    total_elevation_gain REAL DEFAULT 0,
    elev_high REAL DEFAULT 0,
    elev_low REAL DEFAULT 0,

    start_lat REAL,
    start_lng REAL,
    end_lat REAL,
    end_lng REAL,

    summary_polyline TEXT,

    device_name TEXT,
    kudos_count INTEGER DEFAULT 0,
    description TEXT DEFAULT '',
    calories INTEGER DEFAULT 0,

    splits_metric TEXT,
    laps TEXT,
    best_efforts TEXT,

    hr_stream TEXT,

    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
`

// migrations are idempotent ALTER statements applied after the schema so that
// databases created before a column existed pick it up. SQLite has no
// "ADD COLUMN IF NOT EXISTS", so each error is checked for the "duplicate
// column" message and otherwise ignored.
var migrations = []string{
	`ALTER TABLE runs ADD COLUMN hr_stream TEXT`,
}

// openDB opens (creating if needed) the SQLite database and applies the schema.
func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return nil, err
		}
	}
	return db, nil
}

// seedFromExport parses the embedded strava export and inserts every run with
// INSERT OR IGNORE (so re-runs are idempotent on the unique strava_id).
func seedFromExport(db *sql.DB, raw []byte) error {
	var export stravaExport
	if err := json.Unmarshal(raw, &export); err != nil {
		return err
	}

	const q = `INSERT OR IGNORE INTO runs (
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

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(q)
	if err != nil {
		return err
	}
	defer stmt.Close()

	inserted := 0
	for _, a := range export.Runs {
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

		res, err := stmt.Exec(
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
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("seed: inserted %d new run(s) from export (%d total in export)", inserted, len(export.Runs))
	return nil
}

// latlng safely extracts a [lat, lng] pair, returning nil pointers when absent.
func latlng(arr []float64) (*float64, *float64) {
	if len(arr) < 2 {
		return nil, nil
	}
	lat, lng := arr[0], arr[1]
	return &lat, &lng
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// rawOrNull stores the JSON blob if present, otherwise NULL.
func rawOrNull(r json.RawMessage) interface{} {
	if len(r) == 0 || string(r) == "null" {
		return nil
	}
	return string(r)
}
