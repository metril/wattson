package route

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBRouterRoute(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/brouter" || q.Get("lonlats") != "7.000000,45.000000|7.010000,45.010000" ||
			q.Get("profile") != "trekking" || q.Get("alternativeidx") != "0" || q.Get("format") != "geojson" {
			http.Error(w, "bad query "+r.URL.RawQuery, 400)
			return
		}
		fmt.Fprint(w, `{"features":[{"geometry":{"coordinates":[[7,45,100],[7.005,45.005,110],[7.01,45.01,120]]}}]}`)
	}))
	defer srv.Close()
	pts, err := BRouter{BaseURL: srv.URL, Client: srv.Client()}.Route(context.Background(),
		[]LonLat{{7, 45}, {7.01, 45.01}}, "trekking")
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 3 || pts[0].Ele != 100 || pts[0].Lat != 45 || pts[0].Lon != 7 || pts[2].Ele != 120 {
		t.Fatalf("pts=%v", pts)
	}
}

func TestBRouterNoEle(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"features":[{"geometry":{"coordinates":[[7,45],[7.01,45.01]]}}]}`)
	}))
	defer srv.Close()
	pts, err := BRouter{BaseURL: srv.URL}.Route(context.Background(), []LonLat{{7, 45}, {7.01, 45.01}}, "trekking")
	if err != nil || len(pts) != 2 || !math.IsNaN(pts[0].Ele) {
		t.Fatalf("pts=%v err=%v", pts, err)
	}
}

func TestBRouterError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "datafile E5_N45.rd5 not found", 500)
	}))
	defer srv.Close()
	_, err := BRouter{BaseURL: srv.URL}.Route(context.Background(), []LonLat{{7, 45}, {7.01, 45.01}}, "trekking")
	var be *BRouterError
	if !errors.As(err, &be) || be.Status != 500 || !slices.Equal(be.MissingTiles, []string{"E5_N45"}) {
		t.Fatalf("err=%v", err)
	}
	if be.Error() != "brouter: datafile E5_N45.rd5 not found" {
		t.Fatalf("msg=%q", be.Error())
	}
	var mt *MissingTilesError
	if !errors.As(err, &mt) || !slices.Equal(mt.Tiles, []string{"E5_N45"}) {
		t.Fatalf("no MissingTilesError: %v", err)
	}
}

func TestBRouterErrorBounds(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, "datafile E10_N45.rd5 not found; datafile ../../etc.rd5; datafile E10_N45.rd5; W5_S0.rd5 ")
		fmt.Fprint(w, strings.Repeat("x", 5000))
	}))
	defer srv.Close()
	_, err := BRouter{BaseURL: srv.URL}.Route(context.Background(), []LonLat{{7, 45}, {7.01, 45.01}}, "p")
	var be *BRouterError
	if !errors.As(err, &be) || len([]rune(be.Message)) > 300 || !slices.Equal(be.MissingTiles, []string{"E10_N45"}) {
		t.Fatalf("err=%v tiles=%v", err, be)
	}
}

func TestBRouterBadInput(t *testing.T) {
	t.Parallel()
	b := BRouter{BaseURL: "http://127.0.0.1:1"}
	if _, err := b.Route(context.Background(), []LonLat{{7, 45}}, "p"); err == nil {
		t.Fatal("want error for one waypoint")
	}
	if _, err := b.Route(context.Background(), []LonLat{{7, 45}, {7, 95}}, "p"); err == nil {
		t.Fatal("want error for bad lat")
	}
}

func TestBRouterTransportErrorHidesURL(t *testing.T) {
	t.Parallel()
	b := BRouter{BaseURL: "http://user:secret@127.0.0.1:1"}
	_, err := b.Route(context.Background(), []LonLat{{7, 45}, {7.01, 45.01}}, "p")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err=%v", err)
	}
}

func TestBRouterCtxTimeout(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := BRouter{BaseURL: srv.URL}.Route(ctx, []LonLat{{7, 45}, {7.01, 45.01}}, "trekking")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}
