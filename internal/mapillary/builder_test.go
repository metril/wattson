package mapillary

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/metril/wattson/internal/mapillary/mapillarytest"
	"github.com/metril/wattson/internal/route"
	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

// northRoute is a 990 m straight line due north from 45N 7E.
func northRoute() *route.Profile {
	const n = 100
	p := &route.Profile{StepM: 10, LengthM: 990}
	for i := 0; i < n; i++ {
		p.Lat = append(p.Lat, 45+float64(i)*10/111320)
		p.Lon = append(p.Lon, 7)
		p.Ele = append(p.Ele, 100)
		p.Grade = append(p.Grade, 0)
	}
	return p
}

func fakeImages() []mapillarytest.Image {
	var out []mapillarytest.Image
	for i, lat := range []float64{45.0003, 45.0030, 45.0060} {
		out = append(out, mapillarytest.Image{ID: int64(1000 + i), Lat: lat, Lon: 7.00002, Compass: 2, CapturedAt: 1700000000000, Creator: "ana"})
	}
	return out
}

type bfix struct {
	b     *Builder
	f     *mapillarytest.Fake
	c     *Client
	st    *store.Store
	tok   atomic.Value
	now   atomic.Int64 // unix seconds
	route store.Route
}

func (x *bfix) setToken(s string) { x.tok.Store(s) }

func newBfix(t *testing.T, st *store.Store, f *mapillarytest.Fake) *bfix {
	t.Helper()
	x := &bfix{st: st, f: f}
	x.tok.Store("MLY|9|secret")
	x.now.Store(1_800_000_000)
	x.c = &Client{GraphURL: f.GraphURL(), TilesURL: f.TilesURL(), HTTP: &http.Client{},
		Token:       func(context.Context) (string, error) { return x.tok.Load().(string), nil },
		ImageHostOK: func(string) bool { return true }, sleep: func(time.Duration) {}}
	r, err := st.CreateRoute(context.Background(), store.Route{Name: "n", Source: "gpx", GeoJSON: []byte(`{}`), Points: route.Encode(northRoute())})
	if err != nil {
		t.Fatal(err)
	}
	x.route = r
	x.b = NewBuilder(x.c, st,
		func(context.Context, int64) (*route.Profile, error) { return northRoute(), nil },
		func(tok string) string { return "fp:" + tok },
		func() time.Time { return time.Unix(x.now.Load(), 0) })
	x.b.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := x.b.Close(ctx); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return x
}

// settle polls Get until the state is not building.
func (x *bfix) settle(t *testing.T) Status {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s, err := x.b.Get(context.Background(), x.route.ID)
		if err != nil {
			t.Fatal(err)
		}
		if s.State != "building" {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatal("still building after 2 s")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (x *bfix) get(t *testing.T) Status {
	t.Helper()
	s, err := x.b.Get(context.Background(), x.route.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBuildThenReadyAndCached(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, fakeImages()...)
	x := newBfix(t, storetest.New(t), f)
	if s := x.get(t); s.State != "building" {
		t.Fatalf("first state = %q, want building", s.State)
	}
	s := x.settle(t)
	if s.State != "ready" || len(s.Photos) < 1 || s.BuiltAt.IsZero() {
		t.Fatalf("settled = %+v", s)
	}
	for i := 1; i < len(s.Photos); i++ {
		if s.Photos[i].DM < s.Photos[i-1].DM {
			t.Errorf("photos not sorted by d_m: %+v", s.Photos)
		}
	}
	n := f.TileRequests()
	if n == 0 {
		t.Fatal("no tile requests")
	}
	if s2 := x.get(t); s2.State != "ready" || len(s2.Photos) != len(s.Photos) || f.TileRequests() != n {
		t.Errorf("second get = %s with %d photos, tiles %d -> %d", s2.State, len(s2.Photos), n, f.TileRequests())
	}
}

func TestBuildRebuildsOnTokenChangeAndStale(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, fakeImages()...)
	x := newBfix(t, storetest.New(t), f)
	x.get(t)
	x.settle(t)
	x.setToken("MLY|9|other")
	if s := x.get(t); s.State != "building" {
		t.Fatalf("after token change: %q, want building", s.State)
	}
	if s := x.settle(t); s.State != "ready" {
		t.Fatalf("after rebuild: %q", s.State)
	}
	x.now.Add(29 * 24 * 3600)
	if s := x.get(t); s.State != "ready" {
		t.Errorf("29 days old: %q, want ready", s.State)
	}
	x.now.Add(2 * 24 * 3600)
	if s := x.get(t); s.State != "building" {
		t.Errorf("31 days old: %q, want building", s.State)
	}
	x.settle(t)
}

func TestBuildTokenRejected(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, fakeImages()...)
	f.SetTileStatus(http.StatusUnauthorized)
	x := newBfix(t, storetest.New(t), f)
	x.get(t)
	s := x.settle(t)
	if s.State != "error" || s.Message != "Mapillary rejected the token. Check it in Settings → Integrations." {
		t.Fatalf("got %+v", s)
	}
	// An error row is final for 10 minutes, then retried.
	n := f.TileRequests()
	if s := x.get(t); s.State != "error" || f.TileRequests() != n {
		t.Errorf("within 10 min: %q tiles %d -> %d", s.State, n, f.TileRequests())
	}
	x.now.Add(11 * 60)
	f.SetTileStatus(0)
	if s := x.get(t); s.State != "building" {
		t.Errorf("after 10 min: %q, want building", s.State)
	}
	if s := x.settle(t); s.State != "ready" {
		t.Errorf("retry: %+v", s)
	}
}

func TestBuildRateLimitedAndUnreachable(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, fakeImages()...)
	f.SetTileStatus(http.StatusTooManyRequests)
	x := newBfix(t, storetest.New(t), f)
	x.get(t)
	if s := x.settle(t); s.State != "error" || s.Message != "Mapillary is rate limiting requests. Try again in a few minutes." {
		t.Fatalf("rate limited: %+v", s)
	}
	f.SetTileStatus(http.StatusBadGateway)
	x.now.Add(11 * 60)
	x.get(t)
	s := x.settle(t)
	if s.State != "error" || s.Message != "Mapillary could not be reached. Try again in a few minutes." {
		t.Fatalf("unreachable: %+v", s)
	}
}

func TestBuildNoCoverage(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t) // every tile 404
	x := newBfix(t, storetest.New(t), f)
	x.get(t)
	s := x.settle(t)
	if s.State != "ready" || s.Photos == nil || len(s.Photos) != 0 {
		t.Fatalf("got %+v (photos nil=%v)", s, s.Photos == nil)
	}
}

func TestGetStates(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, fakeImages()...)
	x := newBfix(t, storetest.New(t), f)
	x.setToken("")
	if s := x.get(t); s.State != "disabled" {
		t.Errorf("no token: %q", s.State)
	}
	x.setToken("MLY|9|secret")
	syn, err := x.st.CreateRoute(context.Background(), store.Route{Name: "s", Source: "synthetic", GeoJSON: []byte(`{}`), Points: route.Encode(northRoute())})
	if err != nil {
		t.Fatal(err)
	}
	s, err := x.b.Get(context.Background(), syn.ID)
	if err != nil || s.State != "unavailable" || s.Message != "Synthetic routes have no street photos" {
		t.Errorf("synthetic: %+v, %v", s, err)
	}
	if _, err := x.b.Get(context.Background(), 9999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing route: %v", err)
	}
	if f.TileRequests() != 0 {
		t.Errorf("tiles requested for disabled/unavailable/missing: %d", f.TileRequests())
	}
}

func TestBuildQueueDedupes(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, fakeImages()...)
	release := f.GateTiles()
	x := newBfix(t, storetest.New(t), f)
	for i := 0; i < 5; i++ {
		if s := x.get(t); s.State != "building" {
			t.Fatalf("get %d: %q", i, s.State)
		}
	}
	release()
	x.settle(t)
	tiles := len(Tiles(northRoute(), 14, 25))
	if got := f.TileRequests(); got != tiles {
		t.Errorf("tile requests = %d, want %d (one build)", got, tiles)
	}
}

func TestBuilderCloseStopsWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st := storetest.New(t)
		blocked := make(chan struct{}, 8)
		rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			blocked <- struct{}{}
			<-r.Context().Done()
			return nil, r.Context().Err()
		})
		c := &Client{GraphURL: "http://g.invalid", TilesURL: "http://t.invalid", HTTP: &http.Client{Transport: rt},
			Token: func(context.Context) (string, error) { return "tok", nil }}
		r, err := st.CreateRoute(context.Background(), store.Route{Name: "n", Source: "gpx", GeoJSON: []byte(`{}`), Points: route.Encode(northRoute())})
		if err != nil {
			t.Fatal(err)
		}
		b := NewBuilder(c, st, func(context.Context, int64) (*route.Profile, error) { return northRoute(), nil },
			func(s string) string { return s }, time.Now)
		if s, err := b.Get(context.Background(), r.ID); err != nil || s.State != "building" {
			t.Fatalf("get: %+v %v", s, err)
		}
		<-blocked // a build is stuck on a tile
		synctest.Wait()
		if err := b.Close(context.Background()); err != nil {
			t.Fatalf("close: %v", err)
		}
		if _, err := b.Get(context.Background(), r.ID); !errors.Is(err, ErrBuilderClosed) {
			t.Errorf("Get after Close: %v, want ErrBuilderClosed", err)
		}
		if _, err := st.GetRoutePhotos(context.Background(), r.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("an aborted build was stored: %v", err)
		}
		// synctest.Test fails if any goroutine the builder started is still alive.
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBuilderNeverStoresToken(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, fakeImages()...)
	f.SetTileStatus(http.StatusBadGateway)
	x := newBfix(t, storetest.New(t), f)
	x.get(t)
	s := x.settle(t)
	row, err := x.st.GetRoutePhotos(context.Background(), x.route.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.Message+row.Error, "secret") {
		t.Errorf("token leaked: %+v %+v", s, row)
	}
}
