package route

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// northTrack is a due-north track of n points spaced stepM apart with no elevation.
func northTrack(n int, stepM float64, lat0 float64) []Point {
	pts := make([]Point, n)
	for i := range pts {
		pts[i] = Point{lat0 + float64(i)*stepM/metresPerDegLon, 10, math.NaN()}
	}
	return pts
}

func TestOpenTopoDataFill(t *testing.T) {
	t.Parallel()
	var reqs atomic.Int32
	var maxLocs atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs.Add(1)
		locs := strings.Split(r.URL.Query().Get("locations"), "|")
		if int32(len(locs)) > maxLocs.Load() {
			maxLocs.Store(int32(len(locs)))
		}
		type res struct {
			Elevation float64 `json:"elevation"`
		}
		out := struct {
			Status  string `json:"status"`
			Results []res  `json:"results"`
		}{Status: "OK"}
		for _, l := range locs {
			lat, _ := strconv.ParseFloat(strings.Split(l, ",")[0], 64)
			out.Results = append(out.Results, res{lat * 1000})
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()
	pts := northTrack(2501, 10, 10)
	o := OpenTopoData{BaseURL: srv.URL, Client: srv.Client(), SampleM: 100}
	if err := o.Fill(context.Background(), pts); err != nil {
		t.Fatal(err)
	}
	if reqs.Load() != 3 || maxLocs.Load() > 100 {
		t.Fatalf("reqs=%d maxLocs=%d", reqs.Load(), maxLocs.Load())
	}
	for i, p := range pts {
		if math.Abs(p.Ele-p.Lat*1000) > 0.5 {
			t.Fatalf("pt %d ele=%v want %v", i, p.Ele, p.Lat*1000)
		}
	}
}

func TestOpenTopoDataNullElevations(t *testing.T) {
	t.Parallel()
	// Samples at 0,100,200,300 m: known, null, known, known.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"OK","results":[{"elevation":100},{"elevation":null},{"elevation":120},{"elevation":130}]}`)
	}))
	defer srv.Close()
	pts := northTrack(31, 10, 10)
	if err := (OpenTopoData{BaseURL: srv.URL}).Fill(context.Background(), pts); err != nil {
		t.Fatal(err)
	}
	if pts[0].Ele != 100 || !math.IsNaN(pts[5].Ele) || !math.IsNaN(pts[15].Ele) {
		t.Fatalf("null-bracketed points must stay NaN: %v %v %v", pts[0].Ele, pts[5].Ele, pts[15].Ele)
	}
	if math.Abs(pts[25].Ele-125) > 1 || pts[30].Ele != 130 {
		t.Fatalf("known span: %v", pts[30].Ele)
	}
}

func TestOpenTopoDataMostlyNull(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"OK","results":[{"elevation":100},{"elevation":null},{"elevation":null},{"elevation":null}]}`)
	}))
	defer srv.Close()
	pts := northTrack(31, 10, 10)
	err := PrepareElevation(context.Background(), pts, OpenTopoData{BaseURL: srv.URL})
	if !errors.Is(err, ErrNoElevation) {
		t.Fatalf("err=%v", err)
	}
}

func TestOpenTopoDataSharedLimiter(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		fmt.Fprint(w, `{"status":"OK","results":[{"elevation":1},{"elevation":2}]}`)
	}))
	defer srv.Close()
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o := OpenTopoData{BaseURL: srv.URL, Interval: 30 * time.Millisecond}
			if err := o.Fill(context.Background(), northTrack(3, 10, 10)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })
	if len(times) != 3 || times[1].Sub(times[0]) < 25*time.Millisecond || times[2].Sub(times[1]) < 25*time.Millisecond {
		t.Fatalf("requests not spaced: %v", times)
	}
}

type failFiller struct{ err error }

func (f failFiller) Fill(context.Context, []Point) error { return f.err }

func TestPrepareElevationFillerError(t *testing.T) {
	t.Parallel()
	cause := errors.New("boom")
	err := PrepareElevation(context.Background(), []Point{{64, 10, 5}, {64.1, 10, 6}}, failFiller{cause})
	if !errors.Is(err, ErrElevationUnavailable) || !errors.Is(err, cause) {
		t.Fatalf("err=%v", err)
	}
}

func TestOpenTopoDataStationary(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := len(strings.Split(r.URL.Query().Get("locations"), "|"))
		parts := make([]string, n)
		for i := range parts {
			parts[i] = `{"elevation":42}`
		}
		fmt.Fprintf(w, `{"status":"OK","results":[%s]}`, strings.Join(parts, ","))
	}))
	defer srv.Close()
	o := OpenTopoData{BaseURL: srv.URL}
	// Duplicate trailing points.
	pts := northTrack(5, 10, 10)
	pts = append(pts, pts[4], pts[4], pts[4])
	// Fully stationary track.
	still := []Point{{10, 10, math.NaN()}, {10, 10, math.NaN()}, {10, 10, math.NaN()}}
	for name, p := range map[string][]Point{"trailing": pts, "still": still} {
		if err := PrepareElevation(context.Background(), p, o); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for i, q := range p {
			if q.Ele != 42 {
				t.Fatalf("%s: pt %d ele=%v", name, i, q.Ele)
			}
		}
	}
}

func TestOpenTopoDataError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"INVALID_REQUEST","error":"nope"}`)
	}))
	defer srv.Close()
	pts := northTrack(5, 10, 10)
	if err := (OpenTopoData{BaseURL: srv.URL}).Fill(context.Background(), pts); err == nil {
		t.Fatal("want error")
	}
	if !math.IsNaN(pts[0].Ele) {
		t.Fatal("points must be untouched on error")
	}
}

type fakeFiller struct {
	calls int
	ele   float64
}

func (f *fakeFiller) Fill(_ context.Context, pts []Point) error {
	f.calls++
	for i := range pts {
		pts[i].Ele = f.ele
	}
	return nil
}

func TestPrepareElevationHighLat(t *testing.T) {
	t.Parallel()
	mk := func() []Point {
		return []Point{{64, 10, 50}, {64.001, 10, 51}, {64.002, 10, 52}}
	}
	f := &fakeFiller{ele: 7}
	pts := mk()
	if err := PrepareElevation(context.Background(), pts, f); err != nil || f.calls != 1 || pts[0].Ele != 7 {
		t.Fatalf("calls=%d err=%v", f.calls, err)
	}
	pts = mk()
	if err := PrepareElevation(context.Background(), pts, nil); err != nil || pts[0].Ele != 50 {
		t.Fatalf("nil filler: err=%v", err)
	}
	// Low latitude with elevation present: no-op.
	f = &fakeFiller{}
	if err := PrepareElevation(context.Background(), []Point{{45, 7, 1}, {45.1, 7, 2}}, f); err != nil || f.calls != 0 {
		t.Fatalf("calls=%d err=%v", f.calls, err)
	}
}

func TestPrepareElevationMissing(t *testing.T) {
	t.Parallel()
	mk := func() []Point { return []Point{{45, 7, math.NaN()}, {45.1, 7, math.NaN()}} }
	if err := PrepareElevation(context.Background(), mk(), nil); !errors.Is(err, ErrNoElevation) {
		t.Fatalf("err=%v", err)
	}
	if err := PrepareElevation(context.Background(), mk(), &fakeFiller{ele: 5}); err != nil {
		t.Fatal(err)
	}
}
