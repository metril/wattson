package route

import (
	"bytes"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
)

// synthTrack returns n points spaced ~spacing metres along a gently curving path.
func synthTrack(n int, spacing float64, seed uint64) []Point {
	rng := rand.New(rand.NewPCG(seed, seed+1))
	pts := make([]Point, n)
	lat, lon, heading := 45.0, 7.0, 0.3
	for i := range pts {
		pts[i] = Point{lat, lon, 400 + 150*math.Sin(float64(i)*spacing/4000) + rng.NormFloat64()*0.5}
		heading += (rng.Float64() - 0.5) * 0.02
		lat += spacing * math.Cos(heading) / 111195.08
		lon += spacing * math.Sin(heading) / (111195.08 * math.Cos(lat*math.Pi/180))
	}
	return pts
}

func gpxBytes(pts []Point) []byte {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><gpx version="1.1" creator="bench" xmlns="http://www.topografix.com/GPX/1/1"><trk><name>Bench</name><trkseg>`)
	for _, p := range pts {
		fmt.Fprintf(&sb, `<trkpt lat="%.7f" lon="%.7f"><ele>%.1f</ele></trkpt>`, p.Lat, p.Lon, p.Ele)
	}
	sb.WriteString(`</trkseg></trk></gpx>`)
	return []byte(sb.String())
}

func BenchmarkImportGPX100km(b *testing.B) {
	data := gpxBytes(synthTrack(20000, 5, 7))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pts, _, err := ParseGPX(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
		p, err := Process(pts)
		if err != nil {
			b.Fatal(err)
		}
		if len(Encode(p)) == 0 || len(GeoJSON(p).Coordinates) < 2 {
			b.Fatal("empty output")
		}
	}
}

// TestImportBudget gates the 100 km import at 200 ms/op. It is deliberately
// not parallel: timing a benchmark beside other busy tests would be flaky.
func TestImportBudget(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing budget is only enforced in non-race, non-short runs")
	}
	r := testing.Benchmark(BenchmarkImportGPX100km)
	if r.N == 0 {
		t.Fatal("benchmark did not run")
	}
	t.Logf("100 km import: %.1f ms/op", float64(r.NsPerOp())/1e6)
	if ns := r.NsPerOp(); ns >= 200e6 {
		t.Fatalf("100 km import took %.1f ms/op, budget 200 ms", float64(ns)/1e6)
	}
}
