# Runs Tracker — Google Health ingestion plan

> **Context for a fresh session.** This file is self-contained; assume no prior
> conversation. Project at `/home/coder/runs-tracker/` — Go backend, Vite
> frontend, SQLite, Docker Compose. Build/run with Docker (see bottom).

## Already done (do NOT redo)

The "real vs. estimated data" work is complete and committed (commits `0b6213f`
and `0364596` on `main`):

- **Strava splits are real.** `parseStravaSplits()` in `derive.go` parses the
  stored `splits_metric` JSON into `[]Split`; when present, `SplitsEstimated`
  is set `false`, otherwise we fall back to synthetic `buildSplits()`.
- **Elevation profile.** `buildElevationProfile()` branches: real splits →
  `buildRealElevationProfile()` (sums per-split signed `ElevationDifference`);
  else `buildSyntheticElevationProfile()` (sine wave from elev_high/low).
- **GPX `<ele>` tags.** `buildGPXElevations()` in `gpx.go` interpolates per-point
  elevation from the cumulative per-split `elevation_difference`; only runs when
  `!SplitsEstimated`.
- **UI estimation labels** (`frontend/src/detail.js`, `statsView.js`) are now
  conditional on `splits_estimated`; the "estimated" tag drops for real data.
- **Per-split HR column** is shown in the splits table when any split has HR.
- **Cadence graph** now renders ONLY when a real `cadence_series` is present
  (synthetic cadence generation was removed). Currently NOTHING populates a real
  cadence series, so the cadence graph never shows. Google Health will be the
  first real source (see below).

## Goal

Make `POST /api/ingest/health` ingest **real** Google Health v4 exercise data so
Health-sourced runs fill the UI with real (non-estimated) values, matching what
Strava runs already do. The current `healthActivity` struct in `investigate.go`
is a hand-guessed shape and is WRONG — it must be rewritten to the real schema
documented below.

---

## The Google Health v4 API reality (verified against the docs)

Reference: `https://developers.google.com/health/reference/rest/v4/users.dataTypes.dataPoints`

### Two separate endpoints are required — data is split across them

1. **Exercise data point (JSON)** — `users.dataTypes.dataPoints.get` / `.list`
   with `dataType = exercise`. Carries everything EXCEPT geometry: stats,
   per-split summaries, HR, cadence. One scope:
   `googlehealth.activity_and_fitness.readonly`.

2. **TCX export** — `users.dataTypes.dataPoints.exportExerciseTcx`
   - `GET users/{user}/dataTypes/exercise/dataPoints/{dataPoint}:exportExerciseTcx`
   - Append `?alt=media` for the raw TCX file; without it returns JSON
     `ExportExerciseTcxResponse { tcxData: string }`.
   - This is the ONLY source of GPS route geometry + per-point altitude.
   - Requires **BOTH** scopes: `activity_and_fitness.readonly` AND
     `googlehealth.location.readonly`. Location is a separate user consent — a
     user may grant activity but not location, in which case there is no route.
   - `partialData` (bool) param: include TCX points even when GPS unavailable.

**There is NO location/coordinate data type.** The 37-entry dataPoint value union
contains none. The `Altitude` data type is only `gainMillimeters` (gain, not
position). `Exercise.ExerciseMetadata.hasGps` is just a boolean flag. So the map,
the elevation-profile shape, and GPX `<ele>` are ONLY obtainable via TCX.

### Schema: Exercise (the activity / session)

```
Exercise {
  interval          SessionTimeInterval   // start/end times
  exerciseType      enum                  // Run, etc.
  splits[]          SplitSummary
  splitSummaries[]  SplitSummary          // NOTE: two arrays — confirm which holds per-km running splits vs laps/manual
  exerciseEvents[]  ExerciseEvent
  metricsSummary    MetricsSummary        // aggregate for whole exercise
  exerciseMetadata  ExerciseMetadata      // contains hasGps (bool)
  displayName       string
  activeDuration    string                // protobuf Duration e.g. "3600s" — needs parsing
  notes             string
  updateTime, createTime  string
}
```

`SessionTimeInterval` provides start/end; SplitSummary additionally has
`startTime, startUtcOffset, endTime, endUtcOffset` (real local offset — better
than the current hardcoded "Europe/Amsterdam").

```
SplitSummary {
  startTime, startUtcOffset, endTime, endUtcOffset  string
  activeDuration   string        // Duration
  metricsSummary   MetricsSummary
  splitType        enum (SplitType)   // distance- vs time-based; km vs mile — FILTER on this
}
```

### Schema: MetricsSummary (present at exercise level AND per split)

```
MetricsSummary {
  heartRateZoneDurations  TimeInHeartRateZones
  mobilityMetrics         MobilityMetrics
  caloriesKcal                       number
  distanceMillimeters                number     // ÷ 1e6 → km ; ÷ 1000 → m
  steps                              string(int64)
  averageSpeedMillimetersPerSecond   number     // ÷ 1000 → m/s
  averagePaceSecondsPerMeter         number     // × 1000 → sec/km
  averageHeartRateBeatsPerMinute     string(int64)
  elevationGainMillimeters           number     // GAIN ONLY (positive), ÷1000 → m
  activeZoneMinutes                  string(int64)
  runVo2Max                          number
  totalSwimLengths                   number
}

TimeInHeartRateZones { lightTime, moderateTime, vigorousTime, peakTime : Duration }

MobilityMetrics {
  avgGroundContactTimeDuration       Duration
  avgCadenceStepsPerMinute           number     // <-- DIRECT cadence, use this
  avgStrideLengthMillimeters         string(int64)
  avgVerticalOscillationMillimeters  string(int64)
  avgVerticalRatio                   number      // 5.0–11.0
}
```

> **Units gotcha:** distances are in **millimeters**, speed in **mm/s**, pace in
> **sec/meter**. Several count fields (`steps`, `averageHeartRateBeatsPerMinute`,
> `activeZoneMinutes`, stride/oscillation) are JSON **strings**, not numbers —
> parse them. Durations are protobuf strings like `"3600s"`.

> **Elevation gotcha:** `elevationGainMillimeters` is **positive gain**, not a
> signed per-split delta. You CANNOT reconstruct the up/down profile from it
> (summing gain only ever climbs). Total gain is fine; profile shape needs TCX.

---

## Field → UI mapping (what fills, what doesn't)

| UI element (detail.js) | Source | Real? |
|---|---|---|
| Header name | `displayName` | ✅ |
| Header date/time + tz | `interval.startTime` + `startUtcOffset` | ✅ (use real offset, drop hardcoded Amsterdam) |
| Big stat: Distance | aggregate `distanceMillimeters` | ✅ |
| Big stat: Duration | `activeDuration` | ✅ |
| Big stat: Pace | aggregate `averagePaceSecondsPerMeter` | ✅ |
| Big stat: Elevation (total gain) | aggregate `elevationGainMillimeters` | ✅ |
| Splits table (dist/time/pace/elev/HR) | per-split `MetricsSummary` | ✅ |
| Pace chart | per-split `averagePaceSecondsPerMeter` | ✅ |
| Best efforts 1k/5k/10k | computed from splits | ✅ |
| HR chart | per-split `averageHeartRateBeatsPerMinute` | ✅ (real per-split, replaces synthetic wobble) |
| Cadence chart | per-split `mobilityMetrics.avgCadenceStepsPerMinute` | ✅ (FIRST real cadence source) |
| **Map (route)** | TCX trackpoints only | ❌ without TCX |
| **Elevation profile chart** | TCX per-point altitude only | ❌ without TCX (gain-only is insufficient) |
| **GPX `<ele>` download** | TCX geometry+altitude only | ❌ without TCX |

---

## Implementation plan

### Stage 1 — Exercise JSON ingestion (the bulk of the win)

1. **Rewrite `healthActivity`** (`investigate.go:96`) to the real Exercise schema
   above: nested `metricsSummary`, `splits[]`/`splitSummaries[]`, `interval`,
   `mobilityMetrics`, `exerciseMetadata.hasGps`, `displayName`, `activeDuration`,
   `notes`, `exerciseType`. Use string types for the int64-as-string fields and
   a Duration parser for `activeDuration`.

2. **Map per-split summaries → `[]Split`** (`models.go` `Split`). For each split
   in the per-km array (filter by `splitType`):
   - `DistanceMeters` = `distanceMillimeters / 1000`
   - `ElapsedSeconds` = parse(`activeDuration`)
   - `PaceSecPerKM` = `averagePaceSecondsPerMeter * 1000` (or derive from dist/time)
   - `AverageHeartrate` = parse(`averageHeartRateBeatsPerMinute`)
   - `ElevationGain` = `elevationGainMillimeters / 1000`
   - `ElevationDifference` = LEAVE 0 / unknown (only have gain, not signed) — see
     elevation note below.
   Then set `SplitsEstimated = false` so the UI treats it as real.

3. **Persist splits.** The store path expects splits as JSON in the
   `splits_metric` column (that's what `parseStravaSplits` reads back). Either
   (a) serialize the mapped `[]Split` into a JSON shape `parseStravaSplits` can
   read, OR (b) add a dedicated health-splits column + parser. Option (a) reuses
   the existing read path — preferred. Confirm the exact JSON keys
   `parseStravaSplits` expects (`split`, `distance`, `elapsed_time`,
   `elevation_difference`, `average_heartrate`) and emit those.

4. **Cadence series (NEW real path).** Nothing currently builds a real
   `CadenceSeries`. Add a builder that emits one `SeriesPt{DistanceKM, Value}`
   per split using cumulative distance + `avgCadenceStepsPerMinute`. Populate
   `Run.CadenceSeries` in `computeDerived()` when real per-split cadence exists.
   The frontend already gates the cadence chart on `cadence_series.length`.

5. **HR series.** Today `buildHRSeries()` is a synthetic wobble. For Health,
   build a real per-split HR series from `averageHeartRateBeatsPerMinute`
   (same shape as cadence). Decide: keep wobble for Strava (no per-point HR) but
   use real per-split for Health — gate on availability.

6. **Aggregate fields** in the `upsertActivity` mapping (`investigate.go`):
   distance, moving/elapsed time (from `activeDuration` and interval),
   avg/max speed, avg/max HR (+`has_heartrate`), `average_cadence`
   (= aggregate `mobilityMetrics.avgCadenceStepsPerMinute`),
   `total_elevation_gain`, calories, name, description (`notes`),
   `activity_type`/`sport_type` from `exerciseType` (STOP hardcoding "Run"),
   timezone/offset from `startUtcOffset` (STOP hardcoding Amsterdam),
   `start_date`/`start_date_local` as distinct values.
   Leave `elev_high`/`elev_low` 0 (not provided) — the synthetic profile fallback
   tolerates this, but it's irrelevant once TCX provides real elevation.

7. Keep the existing `healthStravaID()` FNV-hash idempotency for the synthetic
   `strava_id`.

### Stage 2 — TCX export for map + elevation (separate, optional, needs location scope)

This is what makes the map and real elevation profile appear. Without it,
Health runs are fully usable minus route + elevation shape (correct degradation).

1. Fetch TCX via `exportExerciseTcx?alt=media` (or read `tcxData` from the JSON
   response). Requires the user to have granted `location.readonly` too.

2. **Write a TCX parser** (new file, e.g. `tcx.go`). TCX trackpoints carry
   `LatitudeDegrees`, `LongitudeDegrees`, `AltitudeMeters`. Parse into
   `[]LatLng` + parallel `[]float64` altitudes.

3. **Geometry storage problem:** the runs table stores geometry ONLY as an
   encoded `summary_polyline` (TEXT), and `polyline.go` has `decodePolyline()`
   but **NO encoder**. Options:
   - (a) Write `encodePolyline([]LatLng) string` (Google encoded-polyline algo)
     and store into `summary_polyline` — reuses the whole existing render path
     (cards + detail map). RECOMMENDED.
   - (b) Add a new column for raw coordinates (JSON/GeoJSON) and a new decode
     path. More invasive.

4. **Real per-point elevation profile.** With TCX altitudes you have true
   per-point elevation — richer than Strava's per-split deltas. Either:
   - feed per-point altitude directly into a new `buildElevationProfile` branch
     (most accurate), OR
   - downsample to per-km and store as per-split `ElevationDifference` (signed
     deltas between km marks) to reuse `buildRealElevationProfile` and the
     existing `buildGPXElevations` interpolation. The signed-delta approach also
     fixes the Stage-1 elevation gap (gain-only) for free.

5. **GPX** then works automatically once `summary_polyline` + per-point/per-split
   real elevation exist (`buildGPXElevations` already keys off `!SplitsEstimated`
   and per-split `ElevationDifference`).

### Suggested sequencing

Do Stage 1 first and ship it — it removes nearly all estimation for Health runs
(stats, splits, pace, HR, cadence, best efforts) with a single JSON call. Stage 2
is independent and can follow; it only adds the map + true elevation and depends
on the extra location scope.

---

## Open questions to verify (need a REAL sample payload)

- We have the schema but **no real response sample**. Before coding, capture an
  actual Exercise JSON (and a TCX) from the API to confirm exact JSON field
  casing and which of `splits` vs `splitSummaries` holds per-km running splits.
- Confirm `splitType` enum values to select kilometre splits (vs mile/lap/time).
- Confirm `activeDuration` format (assume protobuf `"<seconds>s"`).
- Confirm whether `interval`/`SessionTimeInterval` uses RFC3339 timestamps.

## Sample data

Old Strava sample referenced at `/tmp/strava-detail-sample.json` (may not persist
across sessions). For Health, obtain a fresh sample from the live API.

## Code conventions / build

- Project lives at `/home/coder/runs-tracker/`. Use `sudo -u coder` for commands
  that need it.
- Build + run with Docker: `docker compose build` then `docker compose up -d`
  (use `--no-cache` on the build if frontend/Go changes aren't picked up).
- App listens on `:8651`. Verify a run detail via
  `curl -s http://localhost:8651/api/runs/<id>`.
- The Dockerfile builds the Vite frontend, embeds `frontend/dist`, then builds
  the Go binary — Go is NOT installed on the host, so use Docker to compile.
- Relevant files: `investigate.go` (ingest endpoints + structs),
  `derive.go` (computeDerived, splits/elevation/series builders),
  `models.go` (Run, Split, stravaActivity), `gpx.go` (GPX export),
  `polyline.go` (decode only — needs encoder for Stage 2), `store.go` (DB
  read/write + stats), `db.go` (schema), `frontend/src/detail.js` +
  `statsView.js` (UI gating on `splits_estimated` / `cadence_series`).
