package main

import (
	"fmt"
	"strings"
)

// buildGPX builds a GPX 1.1 document from a run's decoded polyline.
// When real split elevation data is available, elevation tags are added to
// each track point using linear interpolation from the per-split cumulatives.
func buildGPX(r *Run) string {
	pts := r.Polyline
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<gpx version="1.1" creator="runs-tracker" `+
		`xmlns="http://www.topografix.com/GPX/1/1" `+
		`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" `+
		`xsi:schemaLocation="http://www.topografix.com/GPX/1/1 `+
		`http://www.topografix.com/GPX/1/1/gpx.xsd">`+"\n")
	b.WriteString("  <metadata>\n")
	fmt.Fprintf(&b, "    <name>%s</name>\n", xmlEscape(r.Name))
	if r.StartDate != "" {
		fmt.Fprintf(&b, "    <time>%s</time>\n", xmlEscape(r.StartDate))
	}
	b.WriteString("  </metadata>\n")
	b.WriteString("  <trk>\n")
	fmt.Fprintf(&b, "    <name>%s</name>\n", xmlEscape(r.Name))
	fmt.Fprintf(&b, "    <type>%s</type>\n", xmlEscape(r.SportType))
	b.WriteString("    <trkseg>\n")

	// Build elevation data per track point.
	elems := buildGPXElevations(r, pts)

	for i, p := range pts {
		if i < len(elems) {
			fmt.Fprintf(&b, "      <trkpt lat=\"%.6f\" lon=\"%.6f\"><ele>%.1f</ele></trkpt>\n", p.Lat, p.Lng, elems[i])
		} else {
			fmt.Fprintf(&b, "      <trkpt lat=\"%.6f\" lon=\"%.6f\"></trkpt>\n", p.Lat, p.Lng)
		}
	}
	b.WriteString("    </trkseg>\n")
	b.WriteString("  </trk>\n")
	b.WriteString("</gpx>\n")
	return b.String()
}

// buildGPXElevations returns an elevation (metres) for each track point,
// interpolated from per-split cumulative elevation_difference data.
// Returns nil when real split data is not available.
func buildGPXElevations(r *Run, pts []LatLng) []float64 {
	if r.SplitsEstimated || len(r.Splits) == 0 {
		return nil
	}
	totalDist := cumulativeDistances(pts)
	if len(totalDist) != len(pts) || totalDist[len(totalDist)-1] <= 0 {
		return nil
	}
	// Build cumulative elevation breakpoints from splits.
	type elevBP struct {
		dist float64
		elev float64
	}
	bps := make([]elevBP, 0, len(r.Splits)+1)
	bps = append(bps, elevBP{dist: 0, elev: 0})
	cumDist := 0.0
	cumElev := 0.0
	for _, s := range r.Splits {
		cumDist += s.DistanceMeters
		cumElev += s.ElevationDifference
		bps = append(bps, elevBP{dist: cumDist, elev: cumElev})
	}

	elems := make([]float64, len(pts))
	bpIdx := 0
	for i, d := range totalDist {
		for bpIdx+1 < len(bps) && d > bps[bpIdx+1].dist {
			bpIdx++
		}
		if bpIdx+1 < len(bps) {
			lo, hi := bps[bpIdx], bps[bpIdx+1]
			frac := (d - lo.dist) / (hi.dist - lo.dist)
			if hi.dist-lo.dist <= 0 {
				frac = 0
			}
			elems[i] = lo.elev + frac*(hi.elev-lo.elev)
		} else {
			elems[i] = bps[bpIdx].elev
		}
	}
	return elems
}

func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}