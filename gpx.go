package main

import (
	"fmt"
	"strings"
)

// buildGPX builds a GPX 1.1 document from a run's decoded polyline.
func buildGPX(r *Run) string {
	pts := r.Polyline
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<gpx version="1.1" creator="runs-tracker" ` +
		`xmlns="http://www.topografix.com/GPX/1/1" ` +
		`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" ` +
		`xsi:schemaLocation="http://www.topografix.com/GPX/1/1 ` +
		`http://www.topografix.com/GPX/1/1/gpx.xsd">` + "\n")
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
	for _, p := range pts {
		fmt.Fprintf(&b, "      <trkpt lat=\"%.6f\" lon=\"%.6f\"></trkpt>\n", p.Lat, p.Lng)
	}
	b.WriteString("    </trkseg>\n")
	b.WriteString("  </trk>\n")
	b.WriteString("</gpx>\n")
	return b.String()
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
