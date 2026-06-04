package main

import "encoding/json"

// Run mirrors the `runs` table. Pointer fields are nullable columns.
type Run struct {
	ID           int64  `json:"id"`
	StravaID     int64  `json:"strava_id"`
	Name         string `json:"name"`
	ActivityType string `json:"activity_type"`
	SportType    string `json:"sport_type"`

	StartDateLocal     string `json:"start_date_local"`
	StartDate          string `json:"start_date"`
	Timezone           string `json:"timezone"`
	MovingTimeSeconds  int    `json:"moving_time_seconds"`
	ElapsedTimeSeconds int    `json:"elapsed_time_seconds"`

	DistanceMeters float64 `json:"distance_meters"`
	AverageSpeedMS float64 `json:"average_speed_ms"`
	MaxSpeedMS     float64 `json:"max_speed_ms"`

	HasHeartrate     bool    `json:"has_heartrate"`
	AverageHeartrate float64 `json:"average_heartrate"`
	MaxHeartrate     float64 `json:"max_heartrate"`

	AverageCadence float64 `json:"average_cadence"`

	TotalElevationGain float64 `json:"total_elevation_gain"`
	ElevHigh           float64 `json:"elev_high"`
	ElevLow            float64 `json:"elev_low"`

	StartLat *float64 `json:"start_lat"`
	StartLng *float64 `json:"start_lng"`
	EndLat   *float64 `json:"end_lat"`
	EndLng   *float64 `json:"end_lng"`

	SummaryPolyline string `json:"summary_polyline"`

	DeviceName  string `json:"device_name"`
	KudosCount  int    `json:"kudos_count"`
	Description string `json:"description"`
	Calories    int    `json:"calories"`

	// Raw JSON blobs as stored.
	SplitsMetric string `json:"-"`
	Laps         string `json:"-"`
	BestEfforts  string `json:"-"`

	// Derived / computed fields populated for API responses.
	DistanceKM       float64    `json:"distance_km"`
	PaceMinPerKM     string     `json:"pace_min_per_km"`
	PaceSecPerKM     float64    `json:"pace_sec_per_km"`
	DurationHuman    string     `json:"duration_human"`
	Polyline         []LatLng   `json:"polyline"`
	Splits           []Split    `json:"splits,omitempty"`
	SplitsEstimated  bool       `json:"splits_estimated"`
	ElevationProfile []ElevPt   `json:"elevation_profile,omitempty"`
	CadenceSeries    []SeriesPt `json:"cadence_series,omitempty"`
	HRSeries         []SeriesPt `json:"hr_series,omitempty"`
	BestEffortsCalc  BestEfize  `json:"best_efforts,omitempty"`
}

// Split is one kilometre split.
type Split struct {
	Split             int     `json:"split"`            // 1-based km index
	DistanceMeters    float64 `json:"distance_meters"`  // length of this segment
	ElapsedSeconds    float64 `json:"elapsed_seconds"`  // time for this segment
	PaceSecPerKM      float64 `json:"pace_sec_per_km"`  // pace, seconds per km
	Pace              string  `json:"pace"`             // formatted m:ss
	ElevationGain     float64 `json:"elevation_gain"`   // meters
	AverageHeartrate  float64 `json:"average_heartrate,omitempty"`
}

// ElevPt is one point on the elevation profile.
type ElevPt struct {
	DistanceKM float64 `json:"distance_km"`
	Elevation  float64 `json:"elevation"`
}

// SeriesPt is one point on a generic per-distance series (cadence, HR).
type SeriesPt struct {
	DistanceKM float64 `json:"distance_km"`
	Value      float64 `json:"value"`
}

// BestEfize holds the 1k/5k/10k best-effort times for a run (formatted).
type BestEfize struct {
	OneK  string `json:"1k,omitempty"`
	FiveK string `json:"5k,omitempty"`
	TenK  string `json:"10k,omitempty"`
}

// stravaActivity is the subset of fields we read from strava-export.json.
type stravaActivity struct {
	ID                 int64           `json:"id"`
	Name               string          `json:"name"`
	Distance           float64         `json:"distance"`
	MovingTime         int             `json:"moving_time"`
	ElapsedTime        int             `json:"elapsed_time"`
	TotalElevationGain float64         `json:"total_elevation_gain"`
	Type               string          `json:"type"`
	SportType          string          `json:"sport_type"`
	DeviceName         string          `json:"device_name"`
	StartDate          string          `json:"start_date"`
	StartDateLocal     string          `json:"start_date_local"`
	Timezone           string          `json:"timezone"`
	KudosCount         int             `json:"kudos_count"`
	StartLatLng        []float64       `json:"start_latlng"`
	EndLatLng          []float64       `json:"end_latlng"`
	AverageSpeed       float64         `json:"average_speed"`
	MaxSpeed           float64         `json:"max_speed"`
	AverageCadence     float64         `json:"average_cadence"`
	HasHeartrate       bool            `json:"has_heartrate"`
	AverageHeartrate   float64         `json:"average_heartrate"`
	MaxHeartrate       float64         `json:"max_heartrate"`
	ElevHigh           float64         `json:"elev_high"`
	ElevLow            float64         `json:"elev_low"`
	Description        string          `json:"description"`
	Calories           int             `json:"calories"`
	Map                struct {
		SummaryPolyline string `json:"summary_polyline"`
	} `json:"map"`
	SplitsMetric json.RawMessage `json:"splits_metric"`
	Laps         json.RawMessage `json:"laps"`
	BestEfforts  json.RawMessage `json:"best_efforts"`
}

// stravaExport is the top-level shape of strava-export.json.
type stravaExport struct {
	TotalRuns int              `json:"total_runs"`
	Runs      []stravaActivity `json:"runs"`
}
