package main

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"
)

const runColumns = `id, strava_id, name, activity_type, sport_type,
	start_date_local, start_date, timezone, moving_time_seconds, elapsed_time_seconds,
	distance_meters, average_speed_ms, max_speed_ms,
	has_heartrate, average_heartrate, max_heartrate,
	average_cadence,
	total_elevation_gain, elev_high, elev_low,
	start_lat, start_lng, end_lat, end_lng,
	summary_polyline, device_name, kudos_count, description, calories,
	splits_metric, laps, best_efforts`

// scanRun reads a single row using the runColumns order.
func scanRun(s interface{ Scan(...interface{}) error }) (*Run, error) {
	var r Run
	var hasHR int
	var device, desc, poly sql.NullString
	var splits, laps, best sql.NullString
	err := s.Scan(
		&r.ID, &r.StravaID, &r.Name, &r.ActivityType, &r.SportType,
		&r.StartDateLocal, &r.StartDate, &r.Timezone, &r.MovingTimeSeconds, &r.ElapsedTimeSeconds,
		&r.DistanceMeters, &r.AverageSpeedMS, &r.MaxSpeedMS,
		&hasHR, &r.AverageHeartrate, &r.MaxHeartrate,
		&r.AverageCadence,
		&r.TotalElevationGain, &r.ElevHigh, &r.ElevLow,
		&r.StartLat, &r.StartLng, &r.EndLat, &r.EndLng,
		&poly, &device, &r.KudosCount, &desc, &r.Calories,
		&splits, &laps, &best,
	)
	if err != nil {
		return nil, err
	}
	r.HasHeartrate = hasHR != 0
	r.SummaryPolyline = poly.String
	r.DeviceName = device.String
	r.Description = desc.String
	r.SplitsMetric = splits.String
	r.Laps = laps.String
	r.BestEfforts = best.String
	return &r, nil
}

// sortColumn maps the public sort key to a SQL ORDER BY expression.
func sortColumn(sort string) string {
	switch sort {
	case "distance":
		return "distance_meters"
	case "pace":
		// lower average_speed => slower pace; pace ASC means faster first,
		// so pace maps to average_speed DESC. We invert in the handler.
		return "average_speed_ms"
	case "elevation":
		return "total_elevation_gain"
	default:
		return "start_date_local"
	}
}

// ListParams holds normalized query parameters for listing runs.
type ListParams struct {
	Page    int
	PerPage int
	Sort    string
	Order   string
	Search  string
}

// listRuns returns a page of runs plus the total count.
func listRuns(db *sql.DB, p ListParams) ([]*Run, int, error) {
	where := ""
	args := []interface{}{}
	if p.Search != "" {
		where = "WHERE LOWER(name) LIKE ?"
		args = append(args, "%"+strings.ToLower(p.Search)+"%")
	}

	var total int
	if err := db.QueryRow("SELECT COUNT(*) FROM runs "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	col := sortColumn(p.Sort)
	order := strings.ToUpper(p.Order)
	if order != "ASC" && order != "DESC" {
		order = "DESC"
	}
	// "pace" ascending (fastest first) corresponds to speed descending.
	if p.Sort == "pace" {
		if order == "ASC" {
			order = "DESC"
		} else {
			order = "ASC"
		}
	}

	offset := (p.Page - 1) * p.PerPage
	q := fmt.Sprintf("SELECT %s FROM runs %s ORDER BY %s %s LIMIT ? OFFSET ?",
		runColumns, where, col, order)
	qargs := append(append([]interface{}{}, args...), p.PerPage, offset)

	rows, err := db.Query(q, qargs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	runs := []*Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, 0, err
		}
		computeDerived(r, false)
		runs = append(runs, r)
	}
	return runs, total, rows.Err()
}

// getRun fetches one run by primary id.
func getRun(db *sql.DB, id int64) (*Run, error) {
	row := db.QueryRow("SELECT "+runColumns+" FROM runs WHERE id = ?", id)
	r, err := scanRun(row)
	if err != nil {
		return nil, err
	}
	// hr_stream is a potentially large blob, so it is loaded only here (the
	// detail view) rather than in the list/stats queries via runColumns.
	var hrStream sql.NullString
	_ = db.QueryRow("SELECT hr_stream FROM runs WHERE id = ?", id).Scan(&hrStream)
	r.HRStream = hrStream.String
	computeDerived(r, true)
	return r, nil
}

// Stats is the aggregated statistics payload.
type Stats struct {
	TotalRuns          int        `json:"total_runs"`
	TotalDistanceKM    float64    `json:"total_distance_km"`
	TotalDurationHours float64    `json:"total_duration_hours"`
	AvgPaceMinPerKM    string     `json:"avg_pace_min_per_km"`
	AvgDistanceKM      float64    `json:"avg_distance_km"`
	AvgCadence         float64    `json:"avg_cadence"`
	TotalElevationGain float64    `json:"total_elevation_gain"`
	LongestRunKM       float64    `json:"longest_run_km"`
	FastestPace        string     `json:"fastest_pace_min_per_km"`
	RunsThisMonth      int        `json:"runs_this_month"`
	RunsThisYear       int        `json:"runs_this_year"`
	CurrentStreakDays  int        `json:"current_streak_days"`
	BestEfforts        BestEfize  `json:"best_efforts"`
	HasEstimatedSplits bool       `json:"has_estimated_splits"`
	// Extras used by the stats page charts.
	Progression []ProgPt `json:"progression"`
	Heatmap     []DayCnt `json:"heatmap"`
}

// ProgPt is one run's contribution to progression charts.
type ProgPt struct {
	Date         string  `json:"date"`
	DistanceKM   float64 `json:"distance_km"`
	PaceSecPerKM float64 `json:"pace_sec_per_km"`
	Pace         string  `json:"pace"`
	Elevation    float64 `json:"elevation"`
}

// DayCnt is a calendar-heatmap entry.
type DayCnt struct {
	Date  string `json:"date"` // YYYY-MM-DD
	Count int    `json:"count"`
	KM    float64 `json:"km"`
}

// computeStats aggregates the whole runs table.
func computeStats(db *sql.DB, now time.Time) (*Stats, error) {
	rows, err := db.Query("SELECT " + runColumns + " FROM runs ORDER BY start_date_local ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	st := &Stats{}
	var totalDistance, totalElev, sumCadence float64
	var totalMoving int
	var cadenceCount int
	var fastestPaceSec = math.Inf(1)
	bestOne, bestFive, bestTen := math.Inf(1), math.Inf(1), math.Inf(1)

	dayKM := map[string]float64{}
	dayCount := map[string]int{}

	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		computeDerived(r, false)

		if r.SplitsEstimated {
			st.HasEstimatedSplits = true
		}

		st.TotalRuns++
		totalDistance += r.DistanceMeters
		totalElev += r.TotalElevationGain
		totalMoving += r.MovingTimeSeconds
		if r.AverageCadence > 0 {
			sumCadence += r.AverageCadence
			cadenceCount++
		}
		if r.DistanceKM > st.LongestRunKM {
			st.LongestRunKM = r.DistanceKM
		}
		if r.PaceSecPerKM > 0 && r.PaceSecPerKM < fastestPaceSec {
			fastestPaceSec = r.PaceSecPerKM
		}

		bestOne = minSec(bestOne, r.BestEffortsCalc.OneK)
		bestFive = minSec(bestFive, r.BestEffortsCalc.FiveK)
		bestTen = minSec(bestTen, r.BestEffortsCalc.TenK)

		day := dateOnly(r.StartDateLocal)
		dayKM[day] += r.DistanceKM
		dayCount[day]++

		t := parseDate(r.StartDateLocal)
		if !t.IsZero() {
			if t.Year() == now.Year() {
				st.RunsThisYear++
				if t.Month() == now.Month() {
					st.RunsThisMonth++
				}
			}
		}

		st.Progression = append(st.Progression, ProgPt{
			Date:         day,
			DistanceKM:   r.DistanceKM,
			PaceSecPerKM: r.PaceSecPerKM,
			Pace:         r.PaceMinPerKM,
			Elevation:    round1(r.TotalElevationGain),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	st.TotalDistanceKM = round2(totalDistance / 1000)
	st.TotalDurationHours = round1(float64(totalMoving) / 3600)
	st.TotalElevationGain = round1(totalElev)
	if st.TotalRuns > 0 {
		st.AvgDistanceKM = round2(totalDistance / 1000 / float64(st.TotalRuns))
	}
	if totalDistance > 0 {
		st.AvgPaceMinPerKM = formatPace(float64(totalMoving) / (totalDistance / 1000))
	}
	if cadenceCount > 0 {
		st.AvgCadence = round1(sumCadence / float64(cadenceCount) * 2) // steps/min
	}
	if !math.IsInf(fastestPaceSec, 1) {
		st.FastestPace = formatPace(fastestPaceSec)
	}
	st.BestEfforts = BestEfize{
		OneK:  secToDur(bestOne),
		FiveK: secToDur(bestFive),
		TenK:  secToDur(bestTen),
	}
	st.CurrentStreakDays = currentStreak(dayCount, now)

	for d, c := range dayCount {
		st.Heatmap = append(st.Heatmap, DayCnt{Date: d, Count: c, KM: round2(dayKM[d])})
	}
	return st, nil
}

// minSec returns the smaller of an accumulator (seconds) and a "m:ss"/"h:mm:ss"
// formatted string parsed back into seconds.
func minSec(acc float64, formatted string) float64 {
	s := durToSec(formatted)
	if s <= 0 {
		return acc
	}
	if float64(s) < acc {
		return float64(s)
	}
	return acc
}

func secToDur(s float64) string {
	if math.IsInf(s, 1) || s <= 0 {
		return ""
	}
	return formatDuration(int(math.Round(s)))
}

// durToSec parses "m:ss" or "h:mm:ss" into seconds. Returns 0 on failure.
func durToSec(s string) int {
	if s == "" {
		return 0
	}
	parts := strings.Split(s, ":")
	mult := []int{1, 60, 3600}
	total := 0
	for i := 0; i < len(parts); i++ {
		var v int
		if _, err := fmt.Sscanf(parts[len(parts)-1-i], "%d", &v); err != nil {
			return 0
		}
		if i < len(mult) {
			total += v * mult[i]
		}
	}
	return total
}

func dateOnly(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

func parseDate(ts string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t
		}
	}
	return time.Time{}
}

// currentStreak counts consecutive days with at least one run, ending today or
// yesterday. Returns 0 if the most recent run is older than yesterday.
func currentStreak(dayCount map[string]int, now time.Time) int {
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	cursor := now
	if dayCount[today] == 0 {
		if dayCount[yesterday] == 0 {
			return 0
		}
		cursor = now.AddDate(0, 0, -1)
	}
	streak := 0
	for {
		d := cursor.Format("2006-01-02")
		if dayCount[d] == 0 {
			break
		}
		streak++
		cursor = cursor.AddDate(0, 0, -1)
	}
	return streak
}
