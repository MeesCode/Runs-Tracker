package main

import (
	"encoding/json"
	"testing"
)

func TestBuildHRSeriesFromStream(t *testing.T) {
	// 300 points, HR ramps 100->160, distance 0..6000m.
	n := 300
	hr := make([]float64, n)
	dist := make([]float64, n)
	for i := 0; i < n; i++ {
		hr[i] = 100 + float64(i)*60/float64(n-1)
		dist[i] = float64(i) * 6000 / float64(n-1)
	}
	raw, _ := json.Marshal(storedHRStream{Heartrate: hr, Distance: dist})
	s := buildHRSeriesFromStream(string(raw), 6.0)
	if len(s) != 41 {
		t.Fatalf("expected 41 points, got %d", len(s))
	}
	if s[0].DistanceKM != 0 {
		t.Errorf("first distance want 0, got %v", s[0].DistanceKM)
	}
	if s[len(s)-1].DistanceKM < 5.9 || s[len(s)-1].DistanceKM > 6.0 {
		t.Errorf("last distance want ~6, got %v", s[len(s)-1].DistanceKM)
	}
	if s[0].Value < 100 || s[0].Value > 102 {
		t.Errorf("first HR want ~100, got %v", s[0].Value)
	}
	if s[len(s)-1].Value < 158 || s[len(s)-1].Value > 160 {
		t.Errorf("last HR want ~160, got %v", s[len(s)-1].Value)
	}

	// Short run: fewer points than samples → one point each.
	rawShort, _ := json.Marshal(storedHRStream{Heartrate: []float64{120, 130, 140}, Distance: []float64{0, 500, 1000}})
	short := buildHRSeriesFromStream(string(rawShort), 1.0)
	if len(short) != 3 {
		t.Errorf("short run want 3 points, got %d", len(short))
	}

	// No distance array → even spacing fallback.
	rawNoDist, _ := json.Marshal(storedHRStream{Heartrate: hr})
	nd := buildHRSeriesFromStream(string(rawNoDist), 6.0)
	if len(nd) != 41 || nd[len(nd)-1].DistanceKM != 6.0 {
		t.Errorf("no-dist fallback bad: len=%d last=%v", len(nd), nd[len(nd)-1].DistanceKM)
	}

	// Empty / blank → nil so caller falls back to wobble.
	if buildHRSeriesFromStream(`{"heartrate":[],"distance":[]}`, 5) != nil {
		t.Errorf("empty stream should return nil")
	}
	if buildHRSeriesFromStream("", 5) != nil {
		t.Errorf("blank stream should return nil")
	}
}
