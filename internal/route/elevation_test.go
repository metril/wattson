package route

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := len(strings.Split(r.URL.Query().Get("locations"), "|"))
		parts := make([]string, n)
		for i := range parts {
			parts[i] = `{"elevation":null}`
			if i == 0 || i == n-1 {
				parts[i] = fmt.Sprintf(`{"elevation":%d}`, 100+i*10)
			}
		}
		fmt.Fprintf(w, `{"status":"OK","results":[%s]}`, strings.Join(parts, ","))
	}))
	defer srv.Close()
	pts := northTrack(31, 10, 10) // 300 m -> samples at 0,100,200,300
	if err := (OpenTopoData{BaseURL: srv.URL}).Fill(context.Background(), pts); err != nil {
		t.Fatal(err)
	}
	if pts[0].Ele != 100 || math.Abs(pts[30].Ele-130) > 1e-6 || math.Abs(pts[15].Ele-115) > 0.1 {
		t.Fatalf("ele %v %v %v", pts[0].Ele, pts[15].Ele, pts[30].Ele)
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
