package hass_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/metril/wattson/internal/hass"
	"github.com/metril/wattson/internal/hass/hasstest"
)

const tok = "secret-token"

func dial(t *testing.T, s *hasstest.Server, o hass.Options) *hass.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := hass.Dial(ctx, s.URL(), tok, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func ctx2s(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestWSURL(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"http://ha:8123":            "ws://ha:8123/api/websocket",
		"https://x/ha/":             "wss://x/ha/api/websocket",
		"https://u:p@x:1/?a=b#frag": "wss://x:1/api/websocket",
	} {
		got, err := hass.WSURL(in)
		if err != nil || got != want {
			t.Errorf("WSURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"ftp://x", "ha:8123", "http://", ""} {
		if _, err := hass.WSURL(bad); err == nil {
			t.Errorf("WSURL(%q) should fail", bad)
		}
	}
}

func TestAuthOK(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	c := dial(t, s, hass.Options{})
	if c.HAVersion() != "2026.10.0" {
		t.Fatalf("version %q", c.HAVersion())
	}
	if s.Auths() != 1 {
		t.Fatalf("auths %d", s.Auths())
	}
}

func TestAuthInvalid(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	_, err := hass.Dial(ctx2s(t), s.URL(), "wrong", hass.Options{})
	if !errors.Is(err, hass.ErrAuth) {
		t.Fatalf("err %v", err)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Fatal("error echoes the token")
	}
}

func TestStates(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	s.SetState("number.r", "5", map[string]any{"min": 1, "max": 24})
	st, err := dial(t, s, hass.Options{}).States(ctx2s(t))
	if err != nil || len(st) != 1 {
		t.Fatalf("%v %v", st, err)
	}
	if st[0].EntityID != "number.r" || st[0].State != "5" || st[0].Attributes["min"] != float64(1) || st[0].Attributes["max"] != float64(24) {
		t.Fatalf("%+v", st[0])
	}
}

func TestCallService(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	s.SetState("number.r", "5", nil)
	c := dial(t, s, hass.Options{})
	if err := c.CallService(ctx2s(t), "number", "set_value", "number.r", map[string]any{"value": 7}); err != nil {
		t.Fatal(err)
	}
	want := []hasstest.Call{{Domain: "number", Service: "set_value", EntityID: "number.r", Data: map[string]any{"value": float64(7)}}}
	if got := s.Calls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls %+v", got)
	}
	st, _ := c.States(ctx2s(t))
	if st[0].State != "7" {
		t.Fatalf("set_value did not update the entity: %+v", st)
	}
}

func TestCallServiceError(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	s.FailCalls("service_validation_error", "bad")
	err := dial(t, s, hass.Options{}).CallService(ctx2s(t), "number", "set_value", "number.r", nil)
	var re *hass.ResultError
	if !errors.As(err, &re) || re.Code != "service_validation_error" || re.Message != "bad" {
		t.Fatalf("err %v", err)
	}
	if err.Error() != "home assistant: service_validation_error: bad" {
		t.Fatalf("text %q", err)
	}
}

type recorder struct {
	ch chan hass.State
}

func newRecorder() *recorder        { return &recorder{ch: make(chan hass.State, 32)} }
func (r *recorder) fn(s hass.State) { r.ch <- s }

func (r *recorder) next(t *testing.T) hass.State {
	t.Helper()
	select {
	case s := <-r.ch:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("no state delivered")
		return hass.State{}
	}
}

func (r *recorder) none(t *testing.T) {
	t.Helper()
	select {
	case s := <-r.ch:
		t.Fatalf("unexpected state %+v", s)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSubscribeEntities(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	s.SetState("sensor.p", "150", map[string]any{"unit": "W"})
	s.SetState("sensor.other", "1", nil)
	c := dial(t, s, hass.Options{})
	r := newRecorder()
	if err := c.SubscribeEntities(ctx2s(t), []string{"sensor.p"}, r.fn); err != nil {
		t.Fatal(err)
	}
	if got := r.next(t); got.EntityID != "sensor.p" || got.State != "150" || got.Attributes["unit"] != "W" {
		t.Fatalf("initial %+v", got)
	}
	s.SetState("sensor.p", "160", nil)
	if got := r.next(t); got.State != "160" || got.Attributes["unit"] != "W" {
		t.Fatalf("diff %+v", got)
	}
	s.SetState("sensor.other", "2", nil)
	r.none(t)
}

func TestSubscribeCompressedDiffs(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	s.SetState("sensor.p", "1", map[string]any{"a": 1, "b": 2})
	c := dial(t, s, hass.Options{})
	r := newRecorder()
	if err := c.SubscribeEntities(ctx2s(t), []string{"sensor.p", "sensor.late"}, r.fn); err != nil {
		t.Fatal(err)
	}
	r.next(t)
	// attribute-only change keeps the state
	s.SetState("sensor.p", "1", map[string]any{"a": 9})
	if got := r.next(t); got.State != "1" || got.Attributes["a"] != float64(9) || got.Attributes["b"] != float64(2) {
		t.Fatalf("attr change %+v", got)
	}
	// "-" removes attribute keys
	s.RemoveAttrs("sensor.p", "b")
	if got := r.next(t); got.State != "1" || got.Attributes["a"] != float64(9) || len(got.Attributes) != 1 {
		t.Fatalf("attr removal %+v", got)
	}
	// an entity created later arrives as an add
	s.SetState("sensor.late", "x", map[string]any{"k": "v"})
	if got := r.next(t); got.EntityID != "sensor.late" || got.State != "x" || got.Attributes["k"] != "v" {
		t.Fatalf("late add %+v", got)
	}
	// "r" removes; re-adding delivers a fresh full state without stale attributes
	s.RemoveState("sensor.p")
	r.none(t)
	s.SetState("sensor.p", "7", nil)
	if got := r.next(t); got.EntityID != "sensor.p" || got.State != "7" || len(got.Attributes) != 0 {
		t.Fatalf("re-add %+v", got)
	}
}

func TestUnsubscribeOnContextDone(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	s.SetState("sensor.p", "1", nil)
	c := dial(t, s, hass.Options{})
	r := newRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.SubscribeEntities(ctx, []string{"sensor.p"}, r.fn); err != nil {
		t.Fatal(err)
	}
	r.next(t)
	cancel()
	// unsubscribe_events is asynchronous: updates stop within the deadline and stay stopped.
	deadline := time.Now().Add(2 * time.Second)
	for n := 0; ; n++ {
		s.SetState("sensor.p", strconv.Itoa(n), nil)
		select {
		case <-r.ch:
			if time.Now().After(deadline) {
				t.Fatal("still receiving after ctx done")
			}
			continue
		case <-time.After(30 * time.Millisecond):
		}
		break
	}
	s.SetState("sensor.p", "final", nil)
	r.none(t)
}

func TestPingTimeout(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	c := dial(t, s, hass.Options{PingInterval: 20 * time.Millisecond})
	s.IgnorePings(true)
	select {
	case <-c.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("not closed")
	}
	if !errors.Is(c.Err(), hass.ErrTimeout) {
		t.Fatalf("err %v", c.Err())
	}
}

func TestPingKeepsAlive(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	// A ping unanswered for twice the interval closes the client, so keep the
	// interval wide enough that a busy machine still answers in time.
	c := dial(t, s, hass.Options{PingInterval: 40 * time.Millisecond})
	select {
	case <-c.Done():
		t.Fatalf("closed: %v", c.Err())
	case <-time.After(250 * time.Millisecond): // several pings
	}
	if c.Err() != nil {
		t.Fatal(c.Err())
	}
}

func TestDropClosesDone(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	c := dial(t, s, hass.Options{})
	s.Drop()
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("not closed")
	}
	if !errors.Is(c.Err(), hass.ErrClosed) {
		t.Fatalf("err %v", c.Err())
	}
	if _, err := c.States(ctx2s(t)); !errors.Is(err, hass.ErrClosed) {
		t.Fatalf("States after close: %v", err)
	}
}

func TestCloseIsErrClosed(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	c := dial(t, s, hass.Options{})
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	<-c.Done()
	if !errors.Is(c.Err(), hass.ErrClosed) {
		t.Fatalf("err %v", c.Err())
	}
}

func TestReadLimit(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	s.SetState("sensor.big", "x", map[string]any{"blob": strings.Repeat("a", 2<<20)})
	if _, err := dial(t, s, hass.Options{}).States(ctx2s(t)); err == nil {
		t.Fatal("2 MiB get_states accepted under the default limit")
	}
	st, err := dial(t, s, hass.Options{ReadLimit: 16 << 20}).States(ctx2s(t))
	if err != nil || len(st) != 1 {
		t.Fatalf("%v %v", len(st), err)
	}
}

// Home Assistant rejects a request whose id does not exceed the previous one
// (id_reuse); concurrent requests and pings must still be written in id order.
func TestConcurrentRequestsKeepIDsIncreasing(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	s.SetState("number.r", "5", nil)
	// A long ping interval: under full -race load 800 round trips can delay a
	// pong past a short timeout; this test is about request ids, not pings.
	c := dial(t, s, hass.Options{PingInterval: time.Second})
	ctx := ctx2s(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8*100)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				errs <- c.CallService(ctx, "number", "set_value", "number.r", map[string]any{"value": i + 1})
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if c.Err() != nil {
		t.Fatal(c.Err())
	}
	if n := len(s.Calls()); n != 800 {
		t.Fatalf("calls %d", n)
	}
}

func TestFakeRejectsIDReuse(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	ctx := ctx2s(t)
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(s.URL(), "http")+"/api/websocket", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	read := func() string {
		_, b, err := ws.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	send := func(v string) {
		if err := ws.Write(ctx, websocket.MessageText, []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	read()
	send(`{"type":"auth","access_token":"` + tok + `"}`)
	read()
	send(`{"id":5,"type":"ping"}`)
	if got := read(); !strings.Contains(got, `"pong"`) {
		t.Fatal(got)
	}
	send(`{"id":4,"type":"ping"}`)
	if got := read(); !strings.Contains(got, `id_reuse`) || !strings.Contains(got, `"success":false`) {
		t.Fatal(got)
	}
}

func TestCloseEndsDoneAtOnce(t *testing.T) {
	t.Parallel()
	s := hasstest.Start(t, tok)
	c := dial(t, s, hass.Options{})
	_ = c.Close()
	select {
	case <-c.Done():
	default:
		t.Fatal("Done not closed when Close returned")
	}
	if _, err := c.States(ctx2s(t)); !errors.Is(err, hass.ErrClosed) {
		t.Fatalf("States after Close: %v", err)
	}
}
