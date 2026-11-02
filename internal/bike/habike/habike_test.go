package habike_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/habike"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/hass"
	"github.com/metril/wattson/internal/hass/hasstest"
)

const token = "tok"

func newServer(t *testing.T) *hasstest.Server {
	t.Helper()
	s := hasstest.Start(t, token)
	s.SetState("number.r", "7", map[string]any{"min": 1.0, "max": 24.0})
	s.SetState("sensor.p", "150", nil)
	s.SetState("sensor.c", "80", nil)
	return s
}

func cfgFor(s *hasstest.Server, tok string) habike.Config {
	return habike.Config{
		Dial: func(ctx context.Context) (*hass.Client, error) {
			return hass.Dial(ctx, s.URL(), tok, hass.Options{})
		},
		Resistance: "number.r", Power: "sensor.p", Cadence: "sensor.c",
		MinLevel: 1, MaxLevel: 24, Period: 20 * time.Millisecond,
	}
}

func open(t *testing.T, s *hasstest.Server, c habike.Config) bike.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sess, err := habike.Dial(c)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close(context.Background()) })
	return sess
}

func values(s *hasstest.Server) []float64 {
	var out []float64
	for _, c := range s.Calls() {
		out = append(out, c.Data["value"].(float64))
	}
	return out
}

func TestHADialReadsRange(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	sess := open(t, s, cfgFor(s, token))
	if sess.Protocol() != bike.ProtoHA || sess.MaxLevel() != 24 {
		t.Fatalf("proto %v max %d", sess.Protocol(), sess.MaxLevel())
	}
	c := cfgFor(s, token)
	c.MaxLevel = 0 // rider max unset: the entity's max applies
	if got := open(t, s, c).MaxLevel(); got != 24 {
		t.Fatalf("max %d", got)
	}
}

func TestHASetLevelCallsService(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	sess := open(t, s, cfgFor(s, token))
	ctx := context.Background()
	if got, err := sess.SetLevel(ctx, 9); err != nil || got != 9 {
		t.Fatal(got, err)
	}
	c := s.Calls()
	if len(c) != 1 || c[0].Domain != "number" || c[0].Service != "set_value" || c[0].EntityID != "number.r" || c[0].Data["value"] != 9.0 {
		t.Fatalf("calls %+v", c)
	}
	_, _ = sess.SetLevel(ctx, 9)
	if len(s.Calls()) != 1 {
		t.Fatalf("dedup failed: %+v", s.Calls())
	}
	_, _ = sess.Resend(ctx, 9)
	if len(s.Calls()) != 2 {
		t.Fatalf("resend: %+v", s.Calls())
	}
}

func TestHANeverZero(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	sess := open(t, s, cfgFor(s, token))
	ctx := context.Background()
	for _, in := range []int{0, -3, 99} {
		if _, err := sess.Resend(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	if got := values(s); len(got) != 3 || got[0] != 1 || got[1] != 1 || got[2] != 24 {
		t.Fatalf("values %v", got)
	}

	s2 := newServer(t)
	s2.SetState("number.r", "7", map[string]any{"min": 3.0})
	sess2 := open(t, s2, cfgFor(s2, token))
	if got, err := sess2.SetLevel(ctx, 1); err != nil || got != 3 {
		t.Fatal(got, err)
	}
	for _, v := range append(values(s), values(s2)...) {
		if v < 1 {
			t.Fatalf("level %v below 1", v)
		}
	}
}

func recv(t *testing.T, ch <-chan bike.Telemetry, ok func(bike.Telemetry) bool) bike.Telemetry {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case v, open := <-ch:
			if !open {
				t.Fatal("telemetry closed")
			}
			if ok(v) {
				return v
			}
		case <-deadline:
			t.Fatal("timeout waiting for telemetry")
		}
	}
}

func TestHATelemetry(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	sess := open(t, s, cfgFor(s, token))
	v1 := recv(t, sess.Telemetry(), func(bike.Telemetry) bool { return true })
	if v1.Level != 7 || v1.Cadence != 80 || v1.PowerW != 150 || v1.At.IsZero() {
		t.Fatalf("%+v", v1)
	}
	v2 := recv(t, sess.Telemetry(), func(bike.Telemetry) bool { return true })
	if !v2.At.After(v1.At) {
		t.Fatal("not re-emitted each period")
	}
	s.SetState("sensor.p", "unavailable", nil)
	recv(t, sess.Telemetry(), func(v bike.Telemetry) bool { return v.PowerW == 0 && v.Cadence == 80 && v.Level == 7 })
}

func TestHAResistanceUnavailable(t *testing.T) {
	t.Parallel()
	for _, st := range []string{"unavailable", "unknown"} {
		s := newServer(t)
		s.SetState("number.r", st, nil)
		_, err := habike.Dial(cfgFor(s, token))(context.Background())
		if !errors.Is(err, habike.ErrEntityUnavailable) || !strings.Contains(err.Error(), "number.r") {
			t.Fatalf("%s: %v", st, err)
		}
	}
	s := newServer(t)
	s.RemoveState("number.r")
	if _, err := habike.Dial(cfgFor(s, token))(context.Background()); !errors.Is(err, habike.ErrEntityUnavailable) {
		t.Fatalf("missing: %v", err)
	}
}

func TestHAAuth(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	_, err := habike.Dial(cfgFor(s, "wrong"))(context.Background())
	if !errors.Is(err, hass.ErrAuth) {
		t.Fatalf("got %v", err)
	}
}

func TestHACloseSendsFloor(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	sess := open(t, s, cfgFor(s, token))
	if _, err := sess.SetLevel(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := values(s); len(v) != 2 || v[1] != 1 {
		t.Fatalf("values %v", v)
	}
	select {
	case <-sess.Done():
	default:
		t.Fatal("Done not closed")
	}
	if _, ok := <-sess.Telemetry(); ok {
		// drain buffered samples, then expect closed
		for range sess.Telemetry() {
		}
	}
	if err := sess.Close(context.Background()); err != nil || len(s.Calls()) != 2 {
		t.Fatalf("second close: %v calls %d", err, len(s.Calls()))
	}
	if sess.Err() != nil {
		t.Fatal(sess.Err())
	}
}

func TestHALinkLoss(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	sess := open(t, s, cfgFor(s, token))
	s.Drop()
	select {
	case <-sess.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done not closed")
	}
	if !errors.Is(sess.Err(), blue.ErrDisconnected) {
		t.Fatalf("err %v", sess.Err())
	}
	if _, err := sess.SetLevel(context.Background(), 5); !errors.Is(err, blue.ErrDisconnected) {
		t.Fatalf("set after loss: %v", err)
	}
}

func TestHASupervisorReconnect(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	sup := bike.NewSupervisor(habike.Dial(cfgFor(s, token)), bike.SupervisorOptions{MinLevel: 1, MaxLevel: 24, BackoffMin: 20 * time.Millisecond})
	ctx := context.Background()
	if _, err := sup.SetLevel(ctx, 9); err != nil {
		t.Fatal(err)
	}
	if err := sup.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sup.Close(context.Background()) })
	waitFor(t, func() bool { return s.Auths() == 1 && len(s.Calls()) >= 1 })
	if v := values(s); v[0] != 9 {
		t.Fatalf("values %v", v)
	}
	n := len(s.Calls())
	s.Drop()
	waitFor(t, func() bool { return s.Auths() >= 2 && len(s.Calls()) > n })
	if v := values(s)[n]; v != 9 {
		t.Fatalf("after reconnect first call %v", v)
	}
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
