package maptiles

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/metril/wattson/internal/route"
	"github.com/metril/wattson/internal/settings"
)

// newMgr returns a Manager over a fresh temp dir and the given segments server.
func newMgr(t testing.TB, h http.Handler) (*Manager, string, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	dir := t.TempDir()
	tr := &http.Transport{}
	m := New(func(context.Context) (settings.Settings, error) {
		s := settings.Defaults()
		s.BRouter.SegmentsDir = dir
		s.BRouter.SegmentsURL = srv.URL + "/"
		return s, nil
	}, &http.Client{Transport: tr})
	t.Cleanup(func() { // runs last: server closes after the manager has aborted its requests
		srv.Close()
		tr.CloseIdleConnections()
	})
	t.Cleanup(m.Close)
	return m, dir, srv
}

func body(n int) []byte { return make([]byte, n) }

func serveBytes(n int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body(n))
	})
}

func find(ts []Tile, name string) (Tile, bool) {
	for _, t := range ts {
		if t.Name == name {
			return t, true
		}
	}
	return Tile{}, false
}

// waitFor polls cond for up to 2 s.
func waitFor(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitStatus(t testing.TB, m *Manager, name string, want Status) Tile {
	t.Helper()
	var got Tile
	waitFor(t, name+" "+string(want), func() bool {
		_, ts, err := m.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		got, _ = find(ts, name)
		return got.Status == want
	})
	return got
}

func noPartFiles(t testing.TB, dir string) {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".part") {
			t.Errorf("leftover %s", e.Name())
		}
	}
}

func TestDownloadAtomic(t *testing.T) {
	t.Parallel()
	m, dir, _ := newMgr(t, serveBytes(1<<20))
	if _, err := m.Enqueue(context.Background(), "E5_N45"); err != nil {
		t.Fatal(err)
	}
	tile := waitStatus(t, m, "E5_N45", StatusPresent)
	st, err := os.Stat(filepath.Join(dir, "E5_N45.rd5"))
	if err != nil || st.Size() != 1<<20 || tile.Bytes != 1<<20 {
		t.Fatalf("file = %v %v tile %+v", st, err, tile)
	}
	noPartFiles(t, dir)
	if ok, exists, err := m.Has(context.Background(), "E5_N45"); !ok || !exists || err != nil {
		t.Errorf("Has = %v %v %v", ok, exists, err)
	}
	// Enqueue of a present tile reports it and starts nothing.
	if tl, err := m.Enqueue(context.Background(), "E5_N45"); err != nil || tl.Status != StatusPresent {
		t.Errorf("re-enqueue = %+v %v", tl, err)
	}
}

func TestDownloadProgress(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	m, _, _ := newMgr(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(1<<20))
		_, _ = w.Write(body(512 << 10))
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = w.Write(body(512 << 10))
		case <-r.Context().Done():
		}
	}))
	if _, err := m.Enqueue(context.Background(), "E5_N45"); err != nil {
		t.Fatal(err)
	}
	var got Tile
	waitFor(t, "512 KiB received", func() bool {
		_, ts, _ := m.List(context.Background())
		got, _ = find(ts, "E5_N45")
		return got.Status == StatusDownloading && got.Bytes == 524288
	})
	if got.TotalBytes != 1048576 {
		t.Errorf("total = %d", got.TotalBytes)
	}
	close(release)
	waitStatus(t, m, "E5_N45", StatusPresent)
}

func TestDownload404(t *testing.T) {
	t.Parallel()
	m, dir, _ := newMgr(t, http.NotFoundHandler())
	if _, err := m.Enqueue(context.Background(), "E5_N45"); err != nil {
		t.Fatal(err)
	}
	tile := waitStatus(t, m, "E5_N45", StatusError)
	if tile.Error != "HTTP 404" {
		t.Errorf("error = %q", tile.Error)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("dir not empty: %v", ents)
	}
	// An error can be retried.
	if tl, err := m.Enqueue(context.Background(), "E5_N45"); err != nil || tl.Status != StatusQueued {
		t.Errorf("retry = %+v %v", tl, err)
	}
}

func TestDownloadTruncatedIsError(t *testing.T) {
	t.Parallel()
	m, dir, _ := newMgr(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write(body(10))
	}))
	if _, err := m.Enqueue(context.Background(), "E5_N45"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "E5_N45", StatusError)
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("dir not empty: %v", ents)
	}
}

func TestStalledDownloadAborts(t *testing.T) {
	t.Parallel()
	m, dir, _ := newMgr(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body(1024))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	m.idle = 50 * time.Millisecond
	if _, err := m.Enqueue(context.Background(), "E5_N45"); err != nil {
		t.Fatal(err)
	}
	if tile := waitStatus(t, m, "E5_N45", StatusError); tile.Error != "download stalled" {
		t.Errorf("error = %q", tile.Error)
	}
	noPartFiles(t, dir)
}

func TestRemoveCancels(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 1)
	cancelled := make(chan struct{})
	m, dir, _ := newMgr(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		_, _ = w.Write(body(1024))
		w.(http.Flusher).Flush()
		started <- struct{}{}
		<-r.Context().Done()
		close(cancelled)
	}))
	if _, err := m.Enqueue(context.Background(), "E5_N45"); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := m.Remove(context.Background(), "E5_N45"); err != nil {
		t.Fatal(err)
	}
	_, ts, _ := m.List(context.Background())
	if _, ok := find(ts, "E5_N45"); ok {
		t.Errorf("tile still listed: %+v", ts)
	}
	noPartFiles(t, dir)
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("server request context not cancelled")
	}
}

func TestRemoveDeletesFile(t *testing.T) {
	t.Parallel()
	m, dir, _ := newMgr(t, serveBytes(100))
	if _, err := m.Enqueue(context.Background(), "E5_N45"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, m, "E5_N45", StatusPresent)
	if err := m.Remove(context.Background(), "E5_N45"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "E5_N45.rd5")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still there: %v", err)
	}
	if err := m.Remove(context.Background(), "../x"); !errors.Is(err, route.ErrBadTileName) {
		t.Errorf("bad name = %v", err)
	}
}

func TestQueueSequential(t *testing.T) {
	t.Parallel()
	var cur, peak atomic.Int32
	m, _, _ := newMgr(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := cur.Add(1)
		defer cur.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write(body(100))
	}))
	names := []string{"E5_N45", "E10_N45", "W5_N50"}
	for _, n := range names {
		if _, err := m.Enqueue(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range names {
		waitStatus(t, m, n, StatusPresent)
	}
	if peak.Load() != 1 {
		t.Errorf("max concurrency = %d, want 1", peak.Load())
	}
	_, ts, _ := m.List(context.Background())
	if len(ts) != 3 || ts[0].Name != "E10_N45" { // sorted by name
		t.Errorf("list = %+v", ts)
	}
}

func TestBadTileName(t *testing.T) {
	t.Parallel()
	m, dir, _ := newMgr(t, serveBytes(10))
	for _, n := range []string{"../etc", "E7_N45", "", "E5_N45/../x"} {
		if _, err := m.Enqueue(context.Background(), n); !errors.Is(err, route.ErrBadTileName) {
			t.Errorf("Enqueue(%q) = %v", n, err)
		}
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("dir not empty: %v", ents)
	}
}

func TestDirUnavailable(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "nope")
	m := New(func(context.Context) (settings.Settings, error) {
		s := settings.Defaults()
		s.BRouter.SegmentsDir = missing
		return s, nil
	}, nil)
	t.Cleanup(m.Close)
	if _, err := m.Enqueue(context.Background(), "E5_N45"); !errors.Is(err, ErrDirUnavailable) {
		t.Errorf("Enqueue = %v", err)
	}
	di, ts, err := m.List(context.Background())
	if err != nil || di.Exists || di.Path != missing || len(ts) != 0 {
		t.Errorf("List = %+v %v %v", di, ts, err)
	}
	if ok, exists, err := m.Has(context.Background(), "E5_N45"); ok || exists || err != nil {
		t.Errorf("Has = %v %v %v", ok, exists, err)
	}
}

func TestListShowsPresentOnly(t *testing.T) {
	t.Parallel()
	m, dir, _ := newMgr(t, serveBytes(10))
	for _, f := range []string{"E5_N45.rd5", "readme.rd5", "E7_N45.rd5", ".E10_N45.rd5.part", "W5_N50.rd5"} {
		if err := os.WriteFile(filepath.Join(dir, f), body(3), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	di, ts, err := m.List(context.Background())
	if err != nil || !di.Exists || !di.Writable {
		t.Fatalf("dir = %+v %v", di, err)
	}
	if len(ts) != 2 || ts[0].Name != "E5_N45" || ts[1].Name != "W5_N50" || ts[0].Status != StatusPresent || ts[0].Bytes != 3 {
		t.Errorf("list = %+v", ts)
	}
	if _, err := os.Stat(filepath.Join(dir, writeProbe)); err == nil {
		t.Error("write probe left behind")
	}
}

func TestAvailableParsesIndex(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	m, _, _ := newMgr(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`<a href="W5_N50.rd5">W5_N50.rd5</a> <a href="E5_N45.rd5">x</a>
<a href="readme.txt">r</a> <a href="E7_N45.rd5">bad</a> <a href="E5_N45.rd5">dup</a>`))
	}))
	want := []string{"E5_N45", "W5_N50"}
	for i := 0; i < 2; i++ {
		got, err := m.Available(context.Background())
		if err != nil || strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("Available = %v %v", got, err)
		}
	}
	if hits.Load() != 1 {
		t.Errorf("server hit %d times, want 1", hits.Load())
	}
	// Expires after 24 h.
	m.now = func() time.Time { return time.Now().Add(25 * time.Hour) }
	if _, err := m.Available(context.Background()); err != nil || hits.Load() != 2 {
		t.Errorf("after expiry: hits %d err %v", hits.Load(), err)
	}
}

func TestAvailableError(t *testing.T) {
	t.Parallel()
	m, _, _ := newMgr(t, http.NotFoundHandler())
	if _, err := m.Available(context.Background()); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("Available = %v", err)
	}
}

// Not parallel: goleak inspects every goroutine of the process.
func TestCloseStopsWorker(t *testing.T) {
	defer goleak.VerifyNone(t)
	started := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		_, _ = w.Write(body(10))
		w.(http.Flusher).Flush()
		started <- struct{}{}
		<-r.Context().Done()
	}))
	dir := t.TempDir()
	tr := &http.Transport{}
	m := New(func(context.Context) (settings.Settings, error) {
		s := settings.Defaults()
		s.BRouter.SegmentsDir, s.BRouter.SegmentsURL = dir, srv.URL+"/"
		return s, nil
	}, &http.Client{Transport: tr})
	if _, err := m.Enqueue(context.Background(), "E5_N45"); err != nil {
		t.Fatal(err)
	}
	<-started
	t0 := time.Now()
	m.Close()
	if d := time.Since(t0); d > time.Second {
		t.Errorf("Close took %v", d)
	}
	noPartFiles(t, dir)
	if _, err := m.Enqueue(context.Background(), "E10_N45"); !errors.Is(err, ErrClosed) {
		t.Errorf("Enqueue after Close = %v", err)
	}
	srv.Close()
	tr.CloseIdleConnections()
}
