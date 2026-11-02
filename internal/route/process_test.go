package route

import (
	"bytes"
	"math"
	"math/rand/v2"
	"testing"
)

func loadRamp(t testing.TB) []Point {
	t.Helper()
	pts, _, err := ParseGPX(bytes.NewReader(readFixture(t, "ramp.gpx")))
	if err != nil {
		t.Fatal(err)
	}
	return pts
}

func mustProcess(t testing.TB, pts []Point) *Profile {
	t.Helper()
	p, err := Process(pts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// densify inserts midpoints (halves the spacing) and returns a copy.
func densify(pts []Point) []Point {
	out := make([]Point, 0, 2*len(pts))
	for i, p := range pts {
		out = append(out, p)
		if i+1 < len(pts) {
			q := pts[i+1]
			out = append(out, Point{(p.Lat + q.Lat) / 2, (p.Lon + q.Lon) / 2, (p.Ele + q.Ele) / 2})
		}
	}
	return out
}

func near(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v ± %v", what, got, want, tol)
	}
}

func TestProcessRamp(t *testing.T) {
	t.Parallel()
	p := mustProcess(t, loadRamp(t))
	near(t, "LengthM", p.LengthM, 1000.75, 0.05)
	if p.Len() != 102 {
		t.Fatalf("Len = %d, want 102", p.Len())
	}
	near(t, "Grade[50]", p.Grade[50], 5, 0.3)
	st := p.Stats()
	near(t, "AscentM", st.AscentM, 50, 2)
	near(t, "DescentM", st.DescentM, 0, 0.01)
	near(t, "DistanceM", st.DistanceM, 1000.75, 0.05)
	for i, w := range [4]float64{7, 45, 7, 45.009} {
		near(t, "BBox", st.BBox[i], w, 1e-6)
	}
	near(t, "DistAt(1)", p.DistAt(1), 10, 1e-9)
	near(t, "DistAt(last)", p.DistAt(p.Len()-1), p.LengthM, 1e-9)
	near(t, "Length()", p.Length(), p.LengthM, 0)
}

func TestProcessRecoversNoisyRamp(t *testing.T) {
	t.Parallel()
	// Real tracks never line up with the 10 m resampling grid, so start the
	// 5 m raw spacing 1.3 m off it.
	raw := append([]Point{{0, -1.3 / metresPerDegLon, syntheticStartEle}},
		densify(Synthetic([]Segment{{500, 0}, {2000, 6}, {500, 0}}))...)
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range raw {
		raw[i].Ele += rng.NormFloat64() * 0.5
	}
	p := mustProcess(t, raw)
	// Within GradeWindowM of an end the grade comes from one-sided, edge-fitted
	// samples whose noise is inherently larger, so the flat-noise bound skips them.
	const edge = GradeWindowM
	var sum, maxDev float64
	var n int
	for i := 0; i < p.Len(); i++ {
		d, g := p.DistAt(i), p.Grade[i]
		switch {
		case d >= 700 && d <= 2300:
			dev := math.Abs(g - 6)
			sum += dev
			n++
			maxDev = max(maxDev, dev)
		case (d >= edge && d <= 300) || (d >= 2700 && d <= p.LengthM-edge):
			if math.Abs(g) > 1.0 {
				t.Errorf("flat grade at d=%.0f is %.2f", d, g)
			}
		}
	}
	if mean := sum / float64(n); mean > 0.3 || maxDev > 1.0 {
		t.Errorf("climb: mean dev %.3f (<=0.3), max dev %.3f (<=1.0)", mean, maxDev)
	}
	near(t, "AscentM", p.Stats().AscentM, 120, 3)
}

func TestSpikeRemoved(t *testing.T) {
	t.Parallel()
	// 5 m raw spacing: a spike narrower than the median window must vanish.
	raw := densify(Synthetic([]Segment{{1000, 5}}))
	raw[99].Ele += 80 // between two resample points: raises both neighbours
	p := mustProcess(t, raw)
	for i, g := range p.Grade {
		if math.Abs(g) > 7 {
			t.Fatalf("Grade[%d] = %.2f after spike", i, g)
		}
	}
}

func TestGradeClamped(t *testing.T) {
	t.Parallel()
	up := mustProcess(t, Synthetic([]Segment{{200, 40}}))
	down := mustProcess(t, Synthetic([]Segment{{200, -40}}))
	if mx := slicesMax(up.Grade); mx != MaxGradePct {
		t.Errorf("max = %v", mx)
	}
	if mn := slicesMin(down.Grade); mn != -MaxGradePct {
		t.Errorf("min = %v", mn)
	}
	if st := up.Stats(); st.MaxGradePct != 25 || st.MinGradePct != 25 {
		t.Errorf("stats = %+v", st)
	}
}

func slicesMax(s []float64) float64 {
	m := math.Inf(-1)
	for _, v := range s {
		m = max(m, v)
	}
	return m
}

func slicesMin(s []float64) float64 {
	m := math.Inf(1)
	for _, v := range s {
		m = min(m, v)
	}
	return m
}

func TestProcessErrors(t *testing.T) {
	t.Parallel()
	t.Run("too short", func(t *testing.T) {
		t.Parallel()
		_, err := Process([]Point{{45, 7, 0}, {45 + 30/111195.0, 7, 0}})
		if err != ErrTooShort {
			t.Fatalf("err = %v", err)
		}
		if _, err := Process(nil); err != ErrTooShort {
			t.Fatalf("nil: err = %v", err)
		}
		// Stationary points collapse to zero length.
		if _, err := Process([]Point{{45, 7, 0}, {45, 7, 0}, {45, 7, 0}}); err != ErrTooShort {
			t.Fatalf("stationary: err = %v", err)
		}
	})
	t.Run("too long", func(t *testing.T) {
		t.Parallel()
		_, err := Process([]Point{{0, 0, 0}, {0, 3, 0}, {0, 6, 0}})
		if err != ErrTooLong {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no elevation", func(t *testing.T) {
		t.Parallel()
		pts := loadRamp(t)
		for _, i := range []int{2, 5, 8} {
			pts[i].Ele = math.NaN()
		}
		if _, err := Process(pts); err != ErrNoElevation {
			t.Fatalf("err = %v", err)
		}
		for i := range pts {
			pts[i].Ele = math.NaN()
		}
		if _, err := Process(pts); err != ErrNoElevation {
			t.Fatalf("all NaN: err = %v", err)
		}
	})
	t.Run("one gap interpolated", func(t *testing.T) {
		t.Parallel()
		pts := loadRamp(t)
		pts[5].Ele = math.NaN()
		p := mustProcess(t, pts)
		near(t, "Ele[50]", p.Ele[50], 125, 0.05)
	})
	t.Run("nan ends take nearest", func(t *testing.T) {
		t.Parallel()
		pts := loadRamp(t)
		pts[0].Ele = math.NaN()
		p := mustProcess(t, pts)
		for i, e := range p.Ele {
			if math.IsNaN(e) || math.IsInf(e, 0) {
				t.Fatalf("Ele[%d] = %v", i, e)
			}
		}
	})
}

func TestGradeAtAndPos(t *testing.T) {
	t.Parallel()
	p := mustProcess(t, loadRamp(t))
	last := p.Len() - 1
	if p.GradeAt(-5) != p.GradeAt(0) || p.GradeAt(0) != p.Grade[0]/100 {
		t.Errorf("GradeAt(-5) = %v", p.GradeAt(-5))
	}
	if p.GradeAt(5000) != p.Grade[last]/100 {
		t.Errorf("GradeAt(5000) = %v", p.GradeAt(5000))
	}
	near(t, "GradeAt(505)", p.GradeAt(505), (p.Grade[50]+p.Grade[51])/2/100, 1e-12)
	near(t, "GradeAt(500)", p.GradeAt(500), p.Grade[50]/100, 1e-12)
	// Last interval is shorter than StepM.
	mid := (p.DistAt(last-1) + p.LengthM) / 2
	near(t, "GradeAt(tail mid)", p.GradeAt(mid), (p.Grade[last-1]+p.Grade[last])/2/100, 1e-12)
	lat, lon := p.PosAt(p.LengthM)
	if lat != p.Lat[last] || lon != p.Lon[last] {
		t.Errorf("PosAt(L) = %v,%v", lat, lon)
	}
	lat, lon = p.PosAt(-1)
	if lat != p.Lat[0] || lon != p.Lon[0] {
		t.Errorf("PosAt(-1) = %v,%v", lat, lon)
	}
	lat, lon = p.PosAt(505)
	near(t, "PosAt(505).lat", lat, (p.Lat[50]+p.Lat[51])/2, 1e-12)
	near(t, "PosAt(505).lon", lon, 7, 1e-9)
}

func TestSynthetic(t *testing.T) {
	t.Parallel()
	pts := Synthetic([]Segment{{100, 0}, {200, 5}})
	if got := pts[len(pts)-1].Ele; math.Abs(got-110) > 1e-9 {
		t.Errorf("last ele = %v", got)
	}
	if pts[0].Lat != 0 || pts[0].Lon != 0 || pts[0].Ele != 100 {
		t.Errorf("first = %+v", pts[0])
	}
	var total float64
	for i := 1; i < len(pts); i++ {
		total += haversine(pts[i-1].Lat, pts[i-1].Lon, pts[i].Lat, pts[i].Lon)
	}
	near(t, "length", total, 300, 0.5)
	if len(pts) != 31 {
		t.Errorf("len = %d", len(pts))
	}
	// Non-multiple-of-10 segments keep their exact length and ascent.
	odd := Synthetic([]Segment{{25, 10}})
	if got := odd[len(odd)-1].Ele; math.Abs(got-102.5) > 1e-9 {
		t.Errorf("odd ele = %v", got)
	}
	if Synthetic(nil) != nil {
		t.Error("empty segs")
	}
}

func TestStatsDeadBand(t *testing.T) {
	t.Parallel()
	// Sub-metre wiggle is ignored; real up/down counts once.
	p := &Profile{StepM: 10, LengthM: 90,
		Lat: make([]float64, 10), Lon: make([]float64, 10), Grade: make([]float64, 10),
		Ele: []float64{0, 0.4, 0, 0.4, 5, 5.4, 5, 0, 0.3, 0}}
	st := p.Stats()
	near(t, "ascent", st.AscentM, 5.4, 0.01)
	near(t, "descent", st.DescentM, 5.4, 0.01)
}

func TestCleanRampGradeEverywhere(t *testing.T) {
	t.Parallel()
	// Median and Savitzky-Golay must not bend the ends of a clean slope.
	p := mustProcess(t, Synthetic([]Segment{{1000, 5}}))
	for i, g := range p.Grade {
		if math.Abs(g-5) > 0.1 {
			t.Fatalf("Grade[%d] (d=%.0f) = %.3f, want 5 +- 0.1", i, p.DistAt(i), g)
		}
	}
	short := mustProcess(t, Synthetic([]Segment{{55, -5}}))
	for i, g := range short.Grade {
		if math.Abs(g+5) > 0.1 {
			t.Fatalf("short Grade[%d] = %.3f", i, g)
		}
	}
}

func TestSmoothingHelpers(t *testing.T) {
	t.Parallel()
	// A quadratic is reproduced exactly by the SG filter, ends included.
	x := make([]float64, 40)
	for i := range x {
		x[i] = 3 + 0.5*float64(i) - 0.02*float64(i*i)
	}
	for i, v := range savitzkyGolay(x, 0) {
		near(t, "sg", v, x[i], 1e-9)
	}
	// Median shrinks symmetrically: a monotone series is unchanged.
	m := medianFilter([]float64{1, 2, 3, 4, 5, 6, 7}, MedianWindow)
	for i, v := range m {
		if v != float64(i+1) {
			t.Fatalf("median = %v", m)
		}
	}
}
