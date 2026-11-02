package integrations

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/integrations/mqtttest"
	"github.com/metril/wattson/internal/ridectl"
	"github.com/metril/wattson/internal/settings"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// fakeRide records the commands the bridge sends and feeds it ride events.
type fakeRide struct {
	mu     sync.Mutex
	calls  []string
	err    error
	events chan ridectl.Event
}

func newFakeRide() *fakeRide { return &fakeRide{events: make(chan ridectl.Event, 64)} }

func (f *fakeRide) Do(_ context.Context, c engine.Command) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := string(c.Kind)
	if c.Kind == engine.CmdLevelDelta {
		call += fmt.Sprintf(":%d", c.Value)
	}
	f.calls = append(f.calls, call)
	return f.err
}

func (f *fakeRide) Release(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "release")
	return f.err
}

func (f *fakeRide) Subscribe(int) (<-chan ridectl.Event, func()) { return f.events, func() {} }

func (f *fakeRide) tick(tk engine.Tick) { f.events <- ridectl.Event{Type: "tick", Tick: &tk} }

func (f *fakeRide) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

type rig struct {
	m    *MQTT
	ride *fakeRide
	logs *syncBuf
	stop context.CancelFunc
	done chan error

	mu  sync.Mutex
	cfg settings.MQTT
}

func (r *rig) set(f func(*settings.MQTT)) {
	r.mu.Lock()
	f(&r.cfg)
	r.mu.Unlock()
	r.m.Reload()
}

// newRig starts a bridge against url and stops it when the test ends.
func newRig(t *testing.T, url, user, pass string, opts ...func(*MQTT)) *rig {
	t.Helper()
	r := &rig{ride: newFakeRide(), logs: &syncBuf{}, done: make(chan error, 1)}
	r.cfg = settings.Defaults().MQTT
	r.cfg.URL, r.cfg.Username, r.cfg.Password = url, user, pass
	r.m = NewMQTT(MQTTDeps{
		Settings: func(context.Context) (settings.Settings, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			s := settings.Defaults()
			s.MQTT = r.cfg
			return s, nil
		},
		Ride: r.ride, Version: "1.2.3", ClientID: "wattson-test", RetryDelay: 50 * time.Millisecond,
		Logger: slog.New(slog.NewTextHandler(r.logs, nil)),
	})
	r.m.every = 20 * time.Millisecond
	for _, o := range opts {
		o(r.m)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.stop = cancel
	go func() { r.done <- r.m.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(3 * time.Second):
			t.Error("Run did not return")
		}
	})
	return r
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func retainedIs(b *mqtttest.Broker, topic, want string) func() bool {
	return func() bool { return string(b.Retained(topic)) == want }
}

// ready waits until the bridge has published availability and every discovery
// config, so a test never cuts the connection mid-publish.
func ready(t *testing.T, b *mqtttest.Broker) {
	t.Helper()
	eventually(t, "online", retainedIs(b, "wattson/status", "online"))
	eventually(t, "discovery", func() bool { return b.Retained("homeassistant/button/wattson_release_bike/config") != nil })
}

func TestOnlineAndLWT(t *testing.T) {
	t.Parallel()
	b := mqtttest.Start(t, nil)
	r := newRig(t, b.URL(), "", "")
	ready(t, b)
	w := b.Watch("wattson/status")
	b.KickAll()
	var seen []string
	deadline := time.After(2 * time.Second)
	for !(len(seen) >= 2 && seen[len(seen)-2] == "offline" && seen[len(seen)-1] == "online") {
		select {
		case m := <-w:
			seen = append(seen, string(m.Payload))
		case <-deadline:
			t.Fatalf("status sequence = %v, want ... offline online\n%s", seen, r.logs.String())
		}
	}
}

func TestPublishesState(t *testing.T) {
	t.Parallel()
	b := mqtttest.Start(t, nil)
	r := newRig(t, b.URL(), "", "")
	eventually(t, "online", retainedIs(b, "wattson/status", "online"))
	w := b.Watch("wattson/state")
	a := engine.Tick{State: engine.StateRiding, PowerW: 150}
	r.ride.tick(a)
	r.ride.tick(a)
	r.ride.tick(engine.Tick{State: engine.StateRiding, PowerW: 180})
	var got []string
	timeout := time.After(300 * time.Millisecond)
loop:
	for {
		select {
		case m := <-w:
			got = append(got, string(m.Payload))
		case <-timeout:
			break loop
		}
	}
	if len(got) != 2 || !strings.Contains(got[0], `"power_w":150`) || !strings.Contains(got[1], `"power_w":180`) {
		t.Fatalf("state messages = %v\n%s", got, r.logs.String())
	}
	if !strings.Contains(string(b.Retained("wattson/state")), `"power_w":180`) {
		t.Errorf("retained state = %s", b.Retained("wattson/state"))
	}
}

func TestStateRateLimited(t *testing.T) {
	t.Parallel()
	b := mqtttest.Start(t, nil)
	r := newRig(t, b.URL(), "", "", func(m *MQTT) { m.every = 150 * time.Millisecond })
	eventually(t, "online", retainedIs(b, "wattson/status", "online"))
	w := b.Watch("wattson/state")
	for i := 1; i <= 6; i++ {
		r.ride.tick(engine.Tick{State: engine.StateRiding, PowerW: float64(100 * i)})
	}
	time.Sleep(50 * time.Millisecond)
	n := 0
	for len(w) > 0 {
		<-w
		n++
	}
	if n > 2 {
		t.Errorf("%d state publishes in a burst of 6 ticks, want at most 2 within the first interval", n)
	}
	eventually(t, "last state delivered", func() bool {
		return strings.Contains(string(b.Retained("wattson/state")), `"power_w":600`)
	})
}

func TestCommandsMapToEngine(t *testing.T) {
	t.Parallel()
	b := mqtttest.Start(t, nil)
	r := newRig(t, b.URL(), "", "")
	eventually(t, "online", retainedIs(b, "wattson/status", "online"))
	for _, k := range []string{"start", "pause", "resume", "stop", "level_up", "level_down", "release_bike"} {
		b.Publish("wattson/command/"+k, []byte("PRESS"), false)
	}
	want := []string{"start", "pause", "resume", "stop", "level_delta:1", "level_delta:-1", "release"}
	eventually(t, "7 calls", func() bool { return len(r.ride.Calls()) >= len(want) })
	if got := r.ride.Calls(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func TestStartIsAFreeRide(t *testing.T) {
	t.Parallel()
	var got engine.Command
	f := newFakeRide()
	m := NewMQTT(MQTTDeps{Ride: recorder{f, &got}})
	m.runCommand(context.Background(), "start")
	if got.Kind != engine.CmdStart || got.Mode == nil || got.Mode.Name() != "free" {
		t.Errorf("start sent %+v", got)
	}
}

type recorder struct {
	*fakeRide
	last *engine.Command
}

func (r recorder) Do(_ context.Context, c engine.Command) error { *r.last = c; return nil }

func TestRetainedCommandIgnored(t *testing.T) {
	t.Parallel()
	b := mqtttest.Start(t, nil)
	b.Publish("wattson/command/stop", []byte("PRESS"), true)
	r := newRig(t, b.URL(), "", "")
	eventually(t, "online", retainedIs(b, "wattson/status", "online"))
	b.Publish("wattson/command/pause", []byte("PRESS"), false)
	eventually(t, "the live command", func() bool { return len(r.ride.Calls()) > 0 })
	time.Sleep(50 * time.Millisecond)
	if got := r.ride.Calls(); len(got) != 1 || got[0] != "pause" {
		t.Errorf("calls = %v, want only pause", got)
	}
}

func TestRetainedFlagDropped(t *testing.T) {
	t.Parallel()
	m := NewMQTT(MQTTDeps{Ride: newFakeRide()})
	s := &session{cmds: make(chan string, 4)}
	m.onMessage(s, &paho.Publish{Topic: "wattson/command/stop", Retain: true})
	if len(s.cmds) != 0 {
		t.Error("a retained command was queued")
	}
	m.onMessage(s, &paho.Publish{Topic: "wattson/command/stop"})
	if len(s.cmds) != 1 {
		t.Error("a live command was not queued")
	}
}

func TestUnknownCommandLogged(t *testing.T) {
	t.Parallel()
	b := mqtttest.Start(t, nil)
	r := newRig(t, b.URL(), "", "")
	eventually(t, "online", retainedIs(b, "wattson/status", "online"))
	b.Publish("wattson/command/boost", []byte("PRESS"), false)
	eventually(t, "log", func() bool { return strings.Contains(r.logs.String(), "boost") })
	if c := r.ride.Calls(); len(c) != 0 {
		t.Errorf("calls = %v", c)
	}
	if !r.m.Status().Connected {
		t.Error("connection dropped")
	}
}

func TestCommandErrorLogged(t *testing.T) {
	t.Parallel()
	b := mqtttest.Start(t, nil)
	r := newRig(t, b.URL(), "", "")
	r.ride.err = ridectl.ErrNoBike
	eventually(t, "online", retainedIs(b, "wattson/status", "online"))
	b.Publish("wattson/command/start", []byte("PRESS"), false)
	eventually(t, "log", func() bool { return strings.Contains(r.logs.String(), "no bike") })
	if !strings.Contains(r.logs.String(), "start") {
		t.Errorf("log lacks the command key: %s", r.logs.String())
	}
}

func TestBrokerLateStart(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	r := newRig(t, "mqtt://"+addr, "", "")
	eventually(t, "an error", func() bool { s := r.m.Status(); return s.Error != "" && !s.Connected && s.Enabled })
	ln2, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("port taken meanwhile: %v", err)
	}
	b := mqtttest.StartOn(t, ln2, nil)
	eventually(t, "connected", func() bool { return r.m.Status().Connected })
	eventually(t, "discovery", func() bool { return b.Retained("homeassistant/sensor/wattson_power/config") != nil })
	if s := r.m.Status(); s.Error != "" {
		t.Errorf("error after connecting: %q", s.Error)
	}
}

func TestAuthRejected(t *testing.T) {
	t.Parallel()
	b := mqtttest.Start(t, map[string]string{"u": "p"})
	r := newRig(t, b.URL(), "u", "secretpw")
	eventually(t, "an error", func() bool { return r.m.Status().Error != "" })
	if s := r.m.Status(); s.Connected {
		t.Errorf("status = %+v", s)
	}
	if strings.Contains(r.m.Status().Error+r.logs.String(), "secretpw") {
		t.Error("the password leaked into the status or the log")
	}
	good := newRig(t, b.URL(), "u", "p")
	eventually(t, "login", func() bool { return good.m.Status().Connected })
}

func TestReloadPrefix(t *testing.T) {
	t.Parallel()
	b := mqtttest.Start(t, nil)
	r := newRig(t, b.URL(), "", "")
	const old = "homeassistant/sensor/wattson_power/config"
	eventually(t, "discovery", func() bool { return b.Retained(old) != nil })
	r.set(func(c *settings.MQTT) { c.DiscoveryPrefix = "ha2" })
	eventually(t, "new prefix", func() bool { return b.Retained("ha2/sensor/wattson_power/config") != nil })
	eventually(t, "old prefix cleared", func() bool { return b.Retained(old) == nil })
	if !r.m.Status().Connected {
		t.Error("a prefix change should not reconnect")
	}
}

func TestDisableUnpublishes(t *testing.T) {
	t.Parallel()
	b := mqtttest.Start(t, nil)
	r := newRig(t, b.URL(), "", "")
	const cfg = "homeassistant/sensor/wattson_power/config"
	eventually(t, "discovery", func() bool { return b.Retained(cfg) != nil })
	r.set(func(c *settings.MQTT) { c.URL = "" })
	eventually(t, "configs cleared", func() bool { return b.Retained(cfg) == nil })
	eventually(t, "offline", retainedIs(b, "wattson/status", "offline"))
	if s := r.m.Status(); s.Enabled || s.Connected {
		t.Errorf("status = %+v", s)
	}
	r.set(func(c *settings.MQTT) { c.URL = b.URL() })
	eventually(t, "re-enabled", func() bool { return r.m.Status().Connected && b.Retained(cfg) != nil })
}

func TestIdleWithoutURL(t *testing.T) {
	t.Parallel()
	r := newRig(t, "", "", "")
	time.Sleep(30 * time.Millisecond)
	if s := r.m.Status(); s.Enabled || s.Connected || s.Error != "" {
		t.Errorf("status = %+v", s)
	}
}

func TestRunCancel(t *testing.T) {
	t.Parallel()
	b := mqtttest.Start(t, nil)
	r := newRig(t, b.URL(), "", "")
	eventually(t, "online", retainedIs(b, "wattson/status", "online"))
	start := time.Now()
	r.stop()
	select {
	case err := <-r.done:
		if err != context.Canceled {
			t.Errorf("Run = %v", err)
		}
		r.done <- err
	case <-time.After(time.Second):
		t.Fatal("Run did not return within 1 s")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v", d)
	}
	if got := string(b.Retained("wattson/status")); got != "offline" {
		t.Errorf("status = %q", got)
	}
	if b.Retained("homeassistant/sensor/wattson_power/config") == nil {
		t.Error("discovery should stay retained across a restart")
	}
}

func TestProbeMQTT(t *testing.T) {
	t.Parallel()
	b := mqtttest.Start(t, map[string]string{"u": "p"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := ProbeMQTT(ctx, b.URL(), "u", "p", false); err != nil {
		t.Errorf("good login: %v", err)
	}
	err := ProbeMQTT(ctx, b.URL(), "u", "bad", false)
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("bad login: %v", err)
	}
	if err := ProbeMQTT(ctx, "http://x", "", "", false); err == nil {
		t.Error("a non-mqtt URL was accepted")
	}
}

func TestProbeMQTTSelfSignedTLS(t *testing.T) {
	t.Parallel()
	hs := httptest.NewUnstartedServer(http.NotFoundHandler())
	hs.StartTLS()
	cert := hs.TLS.Certificates
	hs.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mqtttest.StartOn(t, tls.NewListener(ln, &tls.Config{Certificates: cert}), nil)
	url := "mqtts://" + ln.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := ProbeMQTT(ctx, url, "", "", false); err == nil {
		t.Error("a self-signed certificate was accepted without the opt-in")
	}
	if err := ProbeMQTT(ctx, url, "", "", true); err != nil {
		t.Errorf("with tls_insecure_skip_verify: %v", err)
	}
}

func TestCredentialChangeKeepsDiscovery(t *testing.T) {
	t.Parallel()
	b := mqtttest.Start(t, nil)
	r := newRig(t, b.URL(), "", "")
	ready(t, b)
	cfgs := b.Watch("homeassistant/#")
	status := b.Watch("wattson/status")
	r.set(func(c *settings.MQTT) { c.Password = "new" })
	var seen []string
	deadline := time.After(2 * time.Second)
	for !(len(seen) >= 2 && seen[len(seen)-1] == "online" && seen[len(seen)-2] == "offline") {
		select {
		case m := <-status:
			seen = append(seen, string(m.Payload))
		case <-deadline:
			t.Fatalf("status sequence = %v, want a reconnect", seen)
		}
	}
	for len(cfgs) > 0 {
		if m := <-cfgs; len(m.Payload) == 0 {
			t.Errorf("discovery config %s was emptied by a credential change", m.Topic)
		}
	}
	if b.Retained("homeassistant/sensor/wattson_power/config") == nil {
		t.Error("discovery missing after the reconnect")
	}
}
