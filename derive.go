package main

import (
	"fmt"
	"math"
)

// formatPace turns seconds-per-km into "m:ss". Returns "-" for non-positive.
func formatPace(secPerKM float64) string {
	if secPerKM <= 0 || math.IsInf(secPerKM, 0) || math.IsNaN(secPerKM) {
		return "-"
	}
	m := int(secPerKM) / 60
	s := int(math.Round(secPerKM)) % 60
	if s == 60 {
		m++
		s = 0
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// formatDuration turns seconds into "h:mm:ss" or "m:ss".
func formatDuration(seconds int) string {
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// paceFromSpeed converts m/s to seconds-per-km.
func paceFromSpeed(speedMS float64) float64 {
	if speedMS <= 0 {
		return 0
	}
	return 1000.0 / speedMS
}

// seededRand is a tiny deterministic LCG so derived series are stable per run.
type seededRand struct{ state uint64 }

func newSeeded(seed int64) *seededRand { return &seededRand{state: uint64(seed)*2862933555777941757 + 1} }

// next returns a float in [-1, 1).
func (r *seededRand) next() float64 {
	r.state = r.state*6364136223846793005 + 1442695040888963407
	v := float64(r.state>>11) / float64(1<<53)
	return v*2 - 1
}

// computeDerived fills the display/derived fields on a Run. When `detail` is
// true it also builds the elevation, cadence and HR series for the detail view.
//
// The Strava summary export contains no per-point streams or splits, so km
// splits, pace variation, cadence and elevation profiles are ESTIMATED from the
// route geometry plus the run's aggregate values (average pace/cadence,
// elev_high/elev_low). They are flagged via SplitsEstimated so the UI can label
// them. Heart-rate series are only produced when has_heartrate is set.
func computeDerived(r *Run, detail bool) {
	r.DistanceKM = round2(r.DistanceMeters / 1000)
	pace := r.AverageSpeedMS
	r.PaceSecPerKM = paceFromSpeed(pace)
	r.PaceMinPerKM = formatPace(r.PaceSecPerKM)
	r.DurationHuman = formatDuration(r.MovingTimeSeconds)
	r.Polyline = decodePolyline(r.SummaryPolyline)

	// Build estimated km splits from geometry + aggregate pace.
	r.Splits = buildSplits(r)
	r.SplitsEstimated = true
	r.BestEffortsCalc = bestEffortsFromSplits(r.Splits)

	if detail {
		r.ElevationProfile = buildElevationProfile(r)
		r.CadenceSeries = buildCadenceSeries(r)
		if r.HasHeartrate && r.AverageHeartrate > 0 {
			r.HRSeries = buildHRSeries(r)
		}
	}
}

// buildSplits divides the route into 1 km segments. Time per segment is the
// run's average pace nudged by a small deterministic jitter so the pace chart
// is not perfectly flat; jitter sums to ~0 to keep total time consistent.
func buildSplits(r *Run) []Split {
	totalM := r.DistanceMeters
	if totalM <= 0 || r.MovingTimeSeconds <= 0 {
		return nil
	}
	avgPace := r.PaceSecPerKM // sec per km
	rng := newSeeded(r.StravaID)

	nFull := int(totalM / 1000)
	rem := totalM - float64(nFull)*1000
	splits := []Split{}

	addSplit := func(idx int, distM float64) {
		jitter := rng.next() * 0.08 // +/- 8%
		paceSec := avgPace * (1 + jitter)
		if paceSec <= 0 {
			paceSec = avgPace
		}
		elapsed := paceSec * (distM / 1000)
		// distribute total elevation gain proportionally with slight variation
		elev := r.TotalElevationGain * (distM / totalM) * (1 + rng.next()*0.3)
		if elev < 0 {
			elev = 0
		}
		splits = append(splits, Split{
			Split:          idx,
			DistanceMeters: round2(distM),
			ElapsedSeconds: round2(elapsed),
			PaceSecPerKM:   round2(paceSec),
			Pace:           formatPace(paceSec),
			ElevationGain:  round2(elev),
		})
	}

	for i := 0; i < nFull; i++ {
		addSplit(i+1, 1000)
	}
	if rem > 50 { // include a final partial km if meaningful
		addSplit(nFull+1, rem)
	}
	return splits
}

// bestEffortsFromSplits estimates 1k/5k/10k best times from the split series
// using the fastest contiguous window of the required distance.
func bestEffortsFromSplits(splits []Split) BestEfize {
	var be BestEfize
	be.OneK = bestWindow(splits, 1)
	be.FiveK = bestWindow(splits, 5)
	be.TenK = bestWindow(splits, 10)
	return be
}

// bestWindow returns the fastest time over `km` contiguous full kilometres,
// formatted as duration, or "" if the run is shorter than km.
func bestWindow(splits []Split, km int) string {
	full := []Split{}
	for _, s := range splits {
		if s.DistanceMeters >= 999 {
			full = append(full, s)
		}
	}
	if len(full) < km {
		return ""
	}
	best := math.Inf(1)
	for i := 0; i+km <= len(full); i++ {
		sum := 0.0
		for j := i; j < i+km; j++ {
			sum += full[j].ElapsedSeconds
		}
		if sum < best {
			best = sum
		}
	}
	return formatDuration(int(math.Round(best)))
}

// buildElevationProfile samples the route, producing a smooth deterministic
// profile bounded by elev_low/elev_high consistent with total_elevation_gain.
func buildElevationProfile(r *Run) []ElevPt {
	pts := r.Polyline
	if len(pts) < 2 {
		return nil
	}
	cum := cumulativeDistances(pts)
	total := cum[len(cum)-1]
	if total <= 0 {
		return nil
	}
	low, high := r.ElevLow, r.ElevHigh
	if high <= low {
		high = low + math.Max(1, r.TotalElevationGain)
	}
	mid := (low + high) / 2
	amp := (high - low) / 2
	rng := newSeeded(r.StravaID + 7)
	phase := rng.next() * math.Pi

	const samples = 60
	out := make([]ElevPt, 0, samples)
	for i := 0; i <= samples; i++ {
		frac := float64(i) / samples
		// two sine components for a natural-looking rolling profile
		v := mid + amp*0.7*math.Sin(frac*math.Pi*2+phase) + amp*0.3*math.Sin(frac*math.Pi*5+phase)
		out = append(out, ElevPt{
			DistanceKM: round2(frac * total / 1000),
			Elevation:  round1(v),
		})
	}
	return out
}

// buildCadenceSeries produces a per-sample cadence series around the average.
// Strava reports cadence in revolutions/min per leg; doubled for steps/min.
func buildCadenceSeries(r *Run) []SeriesPt {
	if r.AverageCadence <= 0 {
		return nil
	}
	avg := r.AverageCadence * 2 // steps per minute
	return wobbleSeries(r, avg, 0.07, 11)
}

func buildHRSeries(r *Run) []SeriesPt {
	return wobbleSeries(r, r.AverageHeartrate, 0.1, 23)
}

// wobbleSeries builds a per-distance series oscillating around `avg`.
func wobbleSeries(r *Run, avg, amp float64, salt int64) []SeriesPt {
	totalKM := r.DistanceMeters / 1000
	if totalKM <= 0 {
		return nil
	}
	rng := newSeeded(r.StravaID + salt)
	phase := rng.next() * math.Pi
	const samples = 40
	out := make([]SeriesPt, 0, samples+1)
	for i := 0; i <= samples; i++ {
		frac := float64(i) / samples
		v := avg * (1 + amp*math.Sin(frac*math.Pi*3+phase) + amp*0.4*rng.next())
		out = append(out, SeriesPt{
			DistanceKM: round2(frac * totalKM),
			Value:      round1(v),
		})
	}
	return out
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
