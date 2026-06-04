package main

import "math"

// Inline decoder for Google's Encoded Polyline Algorithm Format (precision 5),
// which is what Strava's summary_polyline uses. Kept dependency-free.

// LatLng is a single decoded coordinate pair.
type LatLng struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// decodePolyline decodes an encoded polyline string into a slice of LatLng.
// Returns an empty (non-nil) slice for empty/invalid input.
func decodePolyline(encoded string) []LatLng {
	points := []LatLng{}
	if encoded == "" {
		return points
	}

	var lat, lng int
	index := 0
	length := len(encoded)

	for index < length {
		// Decode latitude delta.
		result, shift := 0, 0
		for {
			if index >= length {
				return points
			}
			b := int(encoded[index]) - 63
			index++
			result |= (b & 0x1f) << shift
			shift += 5
			if b < 0x20 {
				break
			}
		}
		if result&1 != 0 {
			lat += ^(result >> 1)
		} else {
			lat += result >> 1
		}

		// Decode longitude delta.
		result, shift = 0, 0
		for {
			if index >= length {
				return points
			}
			b := int(encoded[index]) - 63
			index++
			result |= (b & 0x1f) << shift
			shift += 5
			if b < 0x20 {
				break
			}
		}
		if result&1 != 0 {
			lng += ^(result >> 1)
		} else {
			lng += result >> 1
		}

		points = append(points, LatLng{
			Lat: float64(lat) / 1e5,
			Lng: float64(lng) / 1e5,
		})
	}

	return points
}

// haversineMeters returns the great-circle distance in meters between two points.
func haversineMeters(a, b LatLng) float64 {
	const r = 6371000.0 // earth radius, meters
	lat1 := a.Lat * math.Pi / 180
	lat2 := b.Lat * math.Pi / 180
	dLat := (b.Lat - a.Lat) * math.Pi / 180
	dLng := (b.Lng - a.Lng) * math.Pi / 180

	s := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(s)))
}

// cumulativeDistances returns, for each point, the cumulative distance in meters
// from the start of the track. The first element is always 0.
func cumulativeDistances(pts []LatLng) []float64 {
	out := make([]float64, len(pts))
	for i := 1; i < len(pts); i++ {
		out[i] = out[i-1] + haversineMeters(pts[i-1], pts[i])
	}
	return out
}
