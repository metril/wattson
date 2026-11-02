package route

import (
	"bytes"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseRamp(t *testing.T) {
	t.Parallel()
	cases := []struct {
		file         string
		format       Format
		degTol, eTol float64
	}{
		{"ramp.gpx", FormatGPX, 1e-6, 0.01},
		{"ramp_rte.gpx", FormatGPX, 1e-6, 0.01},
		{"ramp.tcx", FormatTCX, 1e-6, 0.01},
		{"ramp_course.tcx", FormatTCX, 1e-6, 0.01},
		{"ramp.fit", FormatFIT, 1e-5, 0.2},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			data := readFixture(t, tc.file)
			f, err := Detect(tc.file, data[:min(len(data), 512)])
			if err != nil || f != tc.format {
				t.Fatalf("Detect = %q, %v; want %q", f, err, tc.format)
			}
			pts, name, err := Parse(f, bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if len(pts) != 11 {
				t.Fatalf("len = %d, want 11", len(pts))
			}
			if name != "Ramp" {
				t.Errorf("name = %q", name)
			}
			p0, p10 := pts[0], pts[10]
			if math.Abs(p0.Lat-45) > tc.degTol || math.Abs(p0.Lon-7) > tc.degTol || math.Abs(p0.Ele-100) > tc.eTol {
				t.Errorf("pts[0] = %+v", p0)
			}
			if math.Abs(p10.Lat-45.009) > tc.degTol || math.Abs(p10.Ele-150) > tc.eTol {
				t.Errorf("pts[10] = %+v", p10)
			}
		})
	}
}

func TestParseNoEle(t *testing.T) {
	t.Parallel()
	pts, _, err := ParseGPX(bytes.NewReader(readFixture(t, "ramp_noele.gpx")))
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 11 {
		t.Fatalf("len = %d", len(pts))
	}
	for i, p := range pts {
		if !math.IsNaN(p.Ele) {
			t.Fatalf("pts[%d].Ele = %v, want NaN", i, p.Ele)
		}
	}
}

func TestDetect(t *testing.T) {
	t.Parallel()
	fit := readFixture(t, "ramp.fit")[:14]
	cases := []struct {
		name string
		head string
		want Format
		err  error
	}{
		{"x.bin", string(fit), FormatFIT, nil},
		{"x.txt", `<?xml version="1.0"?><gpx version="1.1">`, FormatGPX, nil},
		{"x.txt", "\xef\xbb\xbf<?xml?><TrainingCenterDatabase>", FormatTCX, nil},
		{"a.tcx", "", FormatTCX, nil},
		{"A.GPX", "", FormatGPX, nil},
		{"a.fit", "", FormatFIT, nil},
		{"a.txt", "hello", "", ErrUnsupportedFormat},
		{"", "", "", ErrUnsupportedFormat},
	}
	for _, tc := range cases {
		got, err := Detect(tc.name, []byte(tc.head))
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Errorf("Detect(%q, %.10q) = %q, %v; want %q, %v", tc.name, tc.head, got, err, tc.want, tc.err)
		}
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	t.Run("waypoints only", func(t *testing.T) {
		t.Parallel()
		g := `<?xml version="1.0"?><gpx version="1.1" xmlns="http://www.topografix.com/GPX/1/1"><wpt lat="1" lon="2"><name>x</name></wpt></gpx>`
		_, _, err := ParseGPX(strings.NewReader(g))
		if !errors.Is(err, ErrNoTrack) {
			t.Fatalf("err = %v, want ErrNoTrack", err)
		}
	})
	t.Run("truncated xml", func(t *testing.T) {
		t.Parallel()
		data := readFixture(t, "ramp.gpx")
		_, _, err := ParseGPX(bytes.NewReader(data[:len(data)/2]))
		if err == nil || errors.Is(err, ErrNoTrack) {
			t.Fatalf("err = %v, want non-ErrNoTrack error", err)
		}
		data = readFixture(t, "ramp.tcx")
		_, _, err = ParseTCX(bytes.NewReader(data[:len(data)/2]))
		if err == nil || errors.Is(err, ErrNoTrack) {
			t.Fatalf("tcx err = %v", err)
		}
	})
	t.Run("random fit", func(t *testing.T) {
		t.Parallel()
		b := make([]byte, 16)
		r := rand.New(rand.NewPCG(3, 4))
		for i := range b {
			b[i] = byte(r.Uint32())
		}
		if _, _, err := ParseFIT(bytes.NewReader(b)); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("empty tcx", func(t *testing.T) {
		t.Parallel()
		_, _, err := ParseTCX(strings.NewReader(`<TrainingCenterDatabase></TrainingCenterDatabase>`))
		if !errors.Is(err, ErrNoTrack) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unsupported format", func(t *testing.T) {
		t.Parallel()
		if _, _, err := Parse("kml", strings.NewReader("")); !errors.Is(err, ErrUnsupportedFormat) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestParseBounds(t *testing.T) {
	t.Parallel()
	lim := limits{points: 20, bytes: 1 << 20}
	rep := func(s string, n int) string { return strings.Repeat(s, n) }
	t.Run("too many tcx points", func(t *testing.T) {
		t.Parallel()
		tp := `<Trackpoint><Position><LatitudeDegrees>1</LatitudeDegrees><LongitudeDegrees>2</LongitudeDegrees></Position></Trackpoint>`
		doc := func(n int) string {
			return `<TrainingCenterDatabase><Courses><Course><Track>` + rep(tp, n) + `</Track></Course></Courses></TrainingCenterDatabase>`
		}
		if _, _, err := parse(FormatTCX, strings.NewReader(doc(21)), lim); !errors.Is(err, ErrTooManyPoints) {
			t.Fatalf("err = %v", err)
		}
		if pts, _, err := parse(FormatTCX, strings.NewReader(doc(20)), lim); err != nil || len(pts) != 20 {
			t.Fatalf("at limit: %d, %v", len(pts), err)
		}
	})
	t.Run("too many gpx points", func(t *testing.T) {
		t.Parallel()
		doc := func(n int) string {
			return `<gpx version="1.1"><trk><trkseg>` + rep(`<trkpt lat="1" lon="2"/>`, n) + `</trkseg></trk></gpx>`
		}
		if _, _, err := parse(FormatGPX, strings.NewReader(doc(21)), lim); !errors.Is(err, ErrTooManyPoints) {
			t.Fatalf("err = %v", err)
		}
		if pts, _, err := parse(FormatGPX, strings.NewReader(doc(20)), lim); err != nil || len(pts) != 20 {
			t.Fatalf("at limit: %d, %v", len(pts), err)
		}
	})
	t.Run("too many fit points", func(t *testing.T) {
		t.Parallel()
		_, _, err := parse(FormatFIT, bytes.NewReader(readFixture(t, "ramp.fit")), limits{points: 5, bytes: 1 << 20})
		if !errors.Is(err, ErrTooManyPoints) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("oversize input", func(t *testing.T) {
		t.Parallel()
		for f, file := range map[Format]string{FormatGPX: "ramp.gpx", FormatTCX: "ramp.tcx", FormatFIT: "ramp.fit"} {
			_, _, err := parse(f, bytes.NewReader(readFixture(t, file)), limits{points: 20, bytes: 100})
			if !errors.Is(err, ErrTooLarge) {
				t.Errorf("%s: err = %v", f, err)
			}
		}
		data := readFixture(t, "ramp.gpx")
		if _, _, err := parse(FormatGPX, bytes.NewReader(data), limits{points: 20, bytes: int64(len(data))}); err != nil {
			t.Errorf("exactly at byte limit: %v", err)
		}
	})
	t.Run("invalid coordinates dropped", func(t *testing.T) {
		t.Parallel()
		g := `<gpx version="1.1"><trk><trkseg>
<trkpt lat="91" lon="2"/><trkpt lat="NaN" lon="2"/><trkpt lat="1" lon="2"><ele>1e300</ele></trkpt></trkseg></trk></gpx>`
		pts, _, err := ParseGPX(strings.NewReader(g))
		if err != nil || len(pts) != 1 || !math.IsNaN(pts[0].Ele) {
			t.Fatalf("pts = %+v, err = %v", pts, err)
		}
	})
}

// Not parallel: it reads process-wide allocation counters.
func TestParseStopsEarlyAndBoundsAllocation(t *testing.T) {
	const filePts = 200_000
	gpxDoc := `<gpx version="1.1"><trk><trkseg>` + strings.Repeat(`<trkpt lat="1" lon="2"><ele>3</ele></trkpt>`, filePts) + `</trkseg></trk></gpx>`
	tcxDoc := `<TrainingCenterDatabase><Courses><Course><Track>` +
		strings.Repeat(`<Trackpoint><Position><LatitudeDegrees>1</LatitudeDegrees><LongitudeDegrees>2</LongitudeDegrees></Position></Trackpoint>`, filePts) +
		`</Track></Course></Courses></TrainingCenterDatabase>`
	for f, doc := range map[Format]string{FormatGPX: gpxDoc, FormatTCX: tcxDoc} {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, _, err := parse(f, strings.NewReader(doc), limits{points: 1000, bytes: MaxFileBytes})
		runtime.ReadMemStats(&after)
		if !errors.Is(err, ErrTooManyPoints) {
			t.Fatalf("%s: err = %v", f, err)
		}
		if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 8<<20 {
			t.Errorf("%s: allocated %d MiB for a %d-point file with a 1000-point limit", f, alloc>>20, filePts)
		}
	}
}

func TestParseDeepNestingIsRejectedQuickly(t *testing.T) {
	t.Parallel()
	deep := strings.Repeat("<a>", 2<<20) // ~6 MiB
	for f, doc := range map[Format]string{
		FormatGPX: `<gpx>` + deep,
		FormatTCX: `<TrainingCenterDatabase>` + deep,
	} {
		start := time.Now()
		_, _, err := Parse(f, strings.NewReader(doc))
		if !errors.Is(err, errTooDeep) {
			t.Errorf("%s: err = %v, want errTooDeep", f, err)
		}
		if el := time.Since(start); el > 2*time.Second {
			t.Errorf("%s: took %v", f, el)
		}
	}
	// 60 levels of unknown wrappers are still fine.
	g := `<gpx>` + strings.Repeat("<x>", 60) + `<trk><trkseg><trkpt lat="1" lon="2"/></trkseg></trk>` + strings.Repeat("</x>", 60) + `</gpx>`
	if pts, _, err := ParseGPX(strings.NewReader(g)); err != nil || len(pts) != 1 {
		t.Errorf("moderate nesting: %d, %v", len(pts), err)
	}
}

func TestParseLatin1AndNames(t *testing.T) {
	t.Parallel()
	g := "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><gpx version=\"1.1\"><metadata><name>Meta</name></metadata>" +
		"<wpt lat=\"5\" lon=\"5\"><name>wpt</name></wpt><trk><name>Caf\xe9</name><trkseg><trkpt lat=\"1\" lon=\"2\"/></trkseg></trk></gpx>"
	pts, name, err := ParseGPX(strings.NewReader(g))
	if err != nil || len(pts) != 1 || name != "Café" {
		t.Fatalf("pts=%d name=%q err=%v", len(pts), name, err)
	}
	_, _, err = ParseGPX(strings.NewReader(`<?xml version="1.0" encoding="KOI8-X"?><gpx/>`))
	if err == nil {
		t.Error("unknown charset accepted")
	}
	// GPX 1.0 root name; rtept ignored when a track exists.
	g = `<gpx version="1.0"><name>Root</name><rte><rtept lat="9" lon="9"/></rte><trk><trkseg><trkpt lat="1" lon="2"/></trkseg></trk></gpx>`
	pts, name, err = ParseGPX(strings.NewReader(g))
	if err != nil || len(pts) != 1 || pts[0].Lat != 1 || name != "Root" {
		t.Fatalf("1.0: %+v %q %v", pts, name, err)
	}
}

func FuzzParse(f *testing.F) {
	for _, n := range []string{"ramp.gpx", "ramp_rte.gpx", "ramp_noele.gpx", "ramp.tcx", "ramp_course.tcx", "ramp.fit"} {
		f.Add(readFixture(f, n))
	}
	f.Add([]byte{})
	f.Add([]byte("<gpx><trk><trkseg><trkpt lat=\"1\" lon=\"1\"/>"))
	f.Fuzz(func(t *testing.T, data []byte) {
		// Raw parsers (no recover in Parse) so library panics surface here.
		for fm, fn := range map[Format]func(io.Reader) ([]Point, string, error){
			FormatGPX: ParseGPX, FormatTCX: ParseTCX, FormatFIT: ParseFIT,
		} {
			pts, _, err := fn(bytes.NewReader(data))
			if err == nil && len(pts) == 0 {
				t.Fatalf("%s: nil error with no points", fm)
			}
		}
		for _, fm := range []Format{FormatGPX, FormatTCX, FormatFIT} {
			_, _, _ = Parse(fm, bytes.NewReader(data))
		}
		_, _ = Detect("x", data[:min(len(data), 512)])
	})
}
