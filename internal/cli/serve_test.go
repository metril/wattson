package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
)

const testPassword = "correct-horse-1"

func TestResolveServeConfig(t *testing.T) {
	t.Parallel()
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		name string
		env  map[string]string
		args []string
		want serveConfig
		rest []string
	}{
		{"defaults", nil, nil, serveConfig{"/data", ":8080"}, nil},
		{"env data dir", map[string]string{"WATTSON_DATA_DIR": "/x"}, nil, serveConfig{"/x", ":8080"}, nil},
		{"env listen", map[string]string{"WATTSON_LISTEN": ":9"}, nil, serveConfig{"/data", ":9"}, nil},
		{"flag beats env", map[string]string{"WATTSON_DATA_DIR": "/x"}, []string{"--data-dir", "/y"}, serveConfig{"/y", ":8080"}, nil},
		{"listen flag", nil, []string{"--listen", "127.0.0.1:9"}, serveConfig{"/data", "127.0.0.1:9"}, nil},
		{"rest", nil, []string{"--listen", ":1", "reset-password", "bob"}, serveConfig{"/data", ":1"}, []string{"reset-password", "bob"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, rest, err := resolveServeConfig(c.args, env(c.env))
			if err != nil || got != c.want || strings.Join(rest, " ") != strings.Join(c.rest, " ") {
				t.Errorf("got %+v %v %v, want %+v %v", got, rest, err, c.want, c.rest)
			}
		})
	}
	if _, _, err := resolveServeConfig([]string{"--bogus"}, env(nil)); err == nil {
		t.Error("unknown flag accepted")
	}
}

// simRec records every simulator session the server dials.
type simRec struct {
	mu   sync.Mutex
	sess []*sim.Session
}

func (r *simRec) dial(c sim.Config) bike.Dial {
	inner := sim.Dial(c)
	return func(ctx context.Context) (bike.Session, error) {
		s, err := inner(ctx)
		if err == nil {
			r.mu.Lock()
			r.sess = append(r.sess, s.(*sim.Session))
			r.mu.Unlock()
		}
		return s, err
	}
}

func (r *simRec) last() *sim.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sess) == 0 {
		return nil
	}
	return r.sess[len(r.sess)-1]
}

// logBuf is a goroutine-safe log sink.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// srv is a running runServer.
type srv struct {
	dir    string
	base   string
	addr   string
	cancel context.CancelFunc
	done   chan error
	logs   *logBuf
	http   *http.Client
}

// startServer runs runServer on dir (127.0.0.1:0) with fast sim clocks and
// waits until it accepts connections.
func startServer(t *testing.T, dir string, rec *simRec, jar http.CookieJar) *srv {
	t.Helper()
	if jar == nil {
		jar, _ = cookiejar.New(nil)
	}
	h := serveHooks{tickEvery: 20 * time.Millisecond}
	if rec != nil {
		h.simDial = rec.dial
	}
	serveTestHooks.Store(dir, h)
	t.Cleanup(func() { serveTestHooks.Delete(dir) })

	ctx, cancel := context.WithCancel(context.Background())
	s := &srv{dir: dir, cancel: cancel, done: make(chan error, 1), logs: &logBuf{},
		http: &http.Client{Jar: jar, Timeout: 5 * time.Second}}
	ready := make(chan string, 1)
	go func() {
		s.done <- runServer(ctx, serveConfig{DataDir: dir, Listen: "127.0.0.1:0"},
			slog.New(slog.NewJSONHandler(s.logs, nil)), func(a string) { ready <- a })
	}()
	select {
	case s.addr = <-ready:
	case err := <-s.done:
		t.Fatalf("server exited early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server not ready")
	}
	s.base = "http://" + s.addr
	t.Cleanup(func() {
		s.cancel()
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
		}
	})
	return s
}

// stop cancels the server and returns runServer's result within 5 s.
func (s *srv) stop(t *testing.T) {
	t.Helper()
	s.cancel()
	select {
	case err := <-s.done:
		if err != nil {
			t.Fatalf("runServer returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runServer did not return within 5 s of cancel")
	}
	s.done <- nil // for the cleanup
}

func (s *srv) do(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, s.base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (s *srv) setup(t *testing.T) {
	t.Helper()
	if code, b := s.do(t, "POST", "/api/setup", map[string]any{"username": "admin", "password": testPassword}); code != 201 {
		t.Fatalf("setup: %d %s", code, b)
	}
}

func (s *srv) bikeState(t *testing.T) string {
	t.Helper()
	code, b := s.do(t, "GET", "/api/bike", nil)
	if code != 200 {
		t.Fatalf("GET /api/bike: %d %s", code, b)
	}
	var st struct{ State string }
	_ = json.Unmarshal(b, &st)
	return st.State
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestServeEndToEnd(t *testing.T) {
	t.Parallel()
	rec := &simRec{}
	s := startServer(t, t.TempDir(), rec, nil)
	if code, b := s.do(t, "GET", "/healthz", nil); code != 200 {
		t.Fatalf("healthz: %d %s", code, b)
	}
	if !strings.Contains(s.logs.String(), `"setup required`) {
		t.Errorf("no setup-required warning in logs: %s", s.logs.String())
	}
	s.setup(t)

	code, b := s.do(t, "GET", "/api/riders", nil)
	var riders []struct{ ID int64 }
	if code != 200 || json.Unmarshal(b, &riders) != nil || len(riders) == 0 {
		t.Fatalf("riders: %d %s", code, b)
	}
	if code, b := s.do(t, "POST", "/api/bike/connect", map[string]any{"rider_id": riders[0].ID}); code != 202 {
		t.Fatalf("connect: %d %s", code, b)
	}
	waitUntil(t, "bike ready", func() bool { return s.bikeState(t) == "ready" })
	if code, b := s.do(t, "POST", "/api/ride/command", map[string]any{"command": "start"}); code != 204 {
		t.Fatalf("start: %d %s", code, b)
	}
	waitUntil(t, "riding", func() bool { return s.bikeState(t) == "riding" })
	sess := rec.last()
	if sess == nil {
		t.Fatal("no sim session captured")
	}

	s.stop(t)
	l := sess.Levels()
	if len(l) == 0 || l[len(l)-1] != 1 {
		t.Errorf("sim levels = %v, want the last to be the floor level 1", l)
	}
	if out := s.logs.String(); !strings.Contains(out, "bike released at its minimum level") {
		t.Errorf("shutdown not logged honestly: %s", out)
	}
}

func TestServeRestartKeepsSessions(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "data") // created by the server
	jar, _ := cookiejar.New(nil)
	s := startServer(t, dir, nil, jar)
	s.setup(t)
	if code, b := s.do(t, "PATCH", "/api/settings", map[string]any{"ha": map[string]any{"token": "t1"}}); code != 200 {
		t.Fatalf("patch settings: %d %s", code, b)
	}
	key1, err := os.ReadFile(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	s.stop(t)

	s2 := startServer(t, dir, nil, jar) // same cookie jar: the old session cookie
	if code, b := s2.do(t, "GET", "/api/auth/me", nil); code != 200 {
		t.Fatalf("me after restart: %d %s", code, b)
	}
	code, b := s2.do(t, "GET", "/api/settings", nil)
	var got struct {
		HA struct {
			TokenSet bool `json:"token_set"`
		} `json:"ha"`
	}
	_ = json.Unmarshal(b, &got)
	if code != 200 || !got.HA.TokenSet {
		t.Errorf("settings after restart: %d %s, want ha.token_set true", code, b)
	}
	if strings.Contains(string(b), "t1") {
		t.Errorf("settings leaked the token: %s", b)
	}
	key2, _ := os.ReadFile(filepath.Join(dir, "secret.key"))
	if !bytes.Equal(key1, key2) {
		t.Error("secret.key changed across restart")
	}
	if fi, err := os.Stat(dir); err != nil {
		t.Errorf("stat data dir: %v", err)
	} else if fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("data dir mode = %v, want no group/other access", fi.Mode())
	}
}

func TestServeBadKeyIsLoud(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret.key"), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runServer(context.Background(), serveConfig{DataDir: dir, Listen: "127.0.0.1:0"}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil)
	if err == nil {
		t.Fatal("runServer accepted a corrupt secret.key")
	}
}

func TestServeListenError(t *testing.T) {
	t.Parallel()
	err := runServer(context.Background(), serveConfig{DataDir: t.TempDir(), Listen: "not-an-address"}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil)
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Errorf("err = %v, want a listen error", err)
	}
}

func TestHealthcheck(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir(), nil, nil)
	_, port, _ := strings.Cut(s.addr, ":")
	d := deps{getenv: func(string) string { return "" }}
	if code, _, errb := runCLI(t, d, "healthcheck", "--listen", ":"+port); code != 0 {
		t.Errorf("healthy server: exit %d, %s", code, errb)
	}
	d.getenv = func(k string) string {
		if k == "WATTSON_LISTEN" {
			return ":" + port
		}
		return ""
	}
	if code, _, errb := runCLI(t, d, "healthcheck"); code != 0 {
		t.Errorf("listen from env: exit %d, %s", code, errb)
	}
	s.stop(t)
	if code, _, _ := runCLI(t, d, "healthcheck"); code != 1 {
		t.Errorf("closed port: exit %d, want 1", code)
	}
}
