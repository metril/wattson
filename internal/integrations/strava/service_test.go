package strava_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/metril/wattson/internal/integrations/strava"
	"github.com/metril/wattson/internal/integrations/strava/stravatest"
	"github.com/metril/wattson/internal/secretbox"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

var (
	key = bytes.Repeat([]byte{3}, 32)
	t0  = time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
)

func TestStateRoundTrip(t *testing.T) {
	t.Parallel()
	in := strava.ConnectState{RiderID: 7, UserID: 3, ReturnTo: "/riders?x=1"}
	got, err := strava.DecodeState(key, strava.EncodeState(key, in, t0), t0.Add(9*time.Minute))
	if err != nil || got != in {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestStateExpired(t *testing.T) {
	t.Parallel()
	v := strava.EncodeState(key, strava.ConnectState{RiderID: 1, UserID: 1}, t0)
	if _, err := strava.DecodeState(key, v, t0.Add(11*time.Minute)); !errors.Is(err, strava.ErrState) {
		t.Errorf("err = %v", err)
	}
}

func TestStateTampered(t *testing.T) {
	t.Parallel()
	v := strava.EncodeState(key, strava.ConnectState{RiderID: 1, UserID: 1}, t0)
	for i := range len(v) {
		b := []byte(v)
		b[i] ^= 1
		if got, err := strava.DecodeState(key, string(b), t0); err == nil {
			t.Fatalf("flipped byte %d accepted: %+v", i, got)
		}
	}
	other := bytes.Repeat([]byte{4}, 32)
	if _, err := strava.DecodeState(other, v, t0); !errors.Is(err, strava.ErrState) {
		t.Errorf("other key: %v", err)
	}
	for _, bad := range []string{"", "x", "a.b", "."} {
		if _, err := strava.DecodeState(key, bad, t0); !errors.Is(err, strava.ErrState) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

type env struct {
	svc  *strava.Service
	st   *store.Store
	set  *settings.Service
	box  *secretbox.Box
	fake *stravatest.Fake
	user store.User
	rid  int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := storetest.New(t)
	box, err := secretbox.NewBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	set := settings.NewService(st, box)
	fake := stravatest.New(t)
	svc := strava.NewService(strava.ServiceDeps{Store: st, Box: box, Settings: set, Client: strava.NewClient(fake.Endpoints(), nil)})
	ctx := context.Background()
	u, err := st.CreateUser(ctx, store.User{Username: "ada"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.CreateRider(ctx, store.DefaultRider(u.ID, "Ada"))
	if err != nil {
		t.Fatal(err)
	}
	return &env{svc: svc, st: st, set: set, box: box, fake: fake, user: u, rid: r.ID}
}

func (e *env) shared(t *testing.T, id, secret string) {
	t.Helper()
	if _, err := e.set.Update(context.Background(), func(s *settings.Settings) error {
		s.Strava.ClientID, s.Strava.ClientSecret = id, secret
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAppForPrecedence(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	if _, _, err := e.svc.AppFor(ctx, e.rid); !errors.Is(err, strava.ErrNotConfigured) {
		t.Fatalf("none: %v", err)
	}
	e.shared(t, "123", "shh")
	if app, own, err := e.svc.AppFor(ctx, e.rid); err != nil || own || app.ClientID != "123" || app.ClientSecret != "shh" {
		t.Fatalf("shared: %+v %v %v", app, own, err)
	}
	cid, sec := "999", "own"
	if _, err := e.svc.UpdateLink(ctx, e.rid, &cid, &sec, nil); err != nil {
		t.Fatal(err)
	}
	if app, own, err := e.svc.AppFor(ctx, e.rid); err != nil || !own || app.ClientID != "999" || app.ClientSecret != "own" {
		t.Fatalf("own: %+v %v %v", app, own, err)
	}
	raw, _ := e.st.StravaLink(ctx, e.rid)
	if !strings.HasPrefix(raw.ClientSecretEnc, "v1.") || strings.Contains(raw.ClientSecretEnc, "own") && raw.ClientSecretEnc == "own" {
		t.Errorf("client secret not sealed: %q", raw.ClientSecretEnc)
	}
	if got, err := e.box.OpenFor(raw.ClientSecretEnc, fmt.Sprintf("strava.rider.%d.client_secret", e.rid)); err != nil || got != "own" {
		t.Errorf("sealed secret opens to %q, %v", got, err)
	}
}

// connect runs the flow against the fake directly through the service.
func (e *env) connect(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	loc, err := e.svc.ConnectURL(ctx, e.rid, e.user.ID, "http://wattson.test/api/strava/callback", "/riders")
	if err != nil {
		t.Fatal(err)
	}
	var state string
	for _, kv := range strings.Split(loc[strings.Index(loc, "?")+1:], "&") {
		if v, ok := strings.CutPrefix(kv, "state="); ok {
			state = v
		}
	}
	if state == "" {
		t.Fatalf("no state in %q", loc)
	}
	if _, err := e.svc.Complete(ctx, state, "c1", "read,activity:write", "http://wattson.test/api/strava/callback", e.user.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSourcePersistsRotatedTokensSealed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.shared(t, "123", "shh")
	e.connect(t)
	ctx := context.Background()
	// Expire the stored access token so the next Token() refreshes.
	l, _ := e.st.StravaLink(ctx, e.rid)
	l.ExpiresAt = time.Now().Add(-time.Hour)
	if err := e.st.PutStravaLink(ctx, l); err != nil {
		t.Fatal(err)
	}
	ts, err := e.svc.Source(ctx, e.rid)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := ts.Token()
	if err != nil || tok.AccessToken != "a2" {
		t.Fatalf("Token = %+v, %v", tok, err)
	}
	l, _ = e.st.StravaLink(ctx, e.rid)
	if got, _ := e.box.OpenFor(l.RefreshEnc, fmt.Sprintf("strava.rider.%d.refresh_token", e.rid)); got != "r2" || !strings.HasPrefix(l.AccessEnc, "v1.") {
		t.Errorf("stored refresh opens to %q (want r2), access %q", got, l.AccessEnc)
	}
	if !l.ExpiresAt.After(time.Now().Add(time.Hour)) {
		t.Errorf("expires_at not updated: %v", l.ExpiresAt)
	}
}

func TestSourceUnauthorizedClearsLink(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.shared(t, "123", "shh")
	e.connect(t)
	ctx := context.Background()
	l, _ := e.st.StravaLink(ctx, e.rid)
	l.ExpiresAt = time.Now().Add(-time.Hour)
	_ = e.st.PutStravaLink(ctx, l)
	ts, err := e.svc.Source(ctx, e.rid)
	if err != nil {
		t.Fatal(err)
	}
	e.fake.SetApp("123", "rotated") // Strava now rejects the stored credentials
	if _, err := ts.Token(); !errors.Is(err, strava.ErrUnauthorized) {
		t.Fatalf("Token err = %v, want ErrUnauthorized", err)
	}
	if l, _ := e.st.StravaLink(ctx, e.rid); l.Connected() || l.AthleteID != 0 {
		t.Errorf("link not cleared: %+v", l)
	}
	if _, err := e.svc.Source(ctx, e.rid); !errors.Is(err, strava.ErrNotConnected) {
		t.Errorf("Source after clear: %v", err)
	}
}

func TestCompleteRefusals(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.shared(t, "123", "shh")
	ctx := context.Background()
	if _, err := e.svc.Complete(ctx, "junk", "c1", strava.Scope, "http://x/cb", e.user.ID); !errors.Is(err, strava.ErrState) {
		t.Errorf("junk state: %v", err)
	}
	loc, _ := e.svc.ConnectURL(ctx, e.rid, e.user.ID, "http://x/cb", "/riders")
	state := loc[strings.Index(loc, "state=")+len("state="):]
	if _, err := e.svc.Complete(ctx, state, "c1", strava.Scope, "http://x/cb", e.user.ID+1); !errors.Is(err, strava.ErrState) {
		t.Errorf("other user: %v", err)
	}
	if _, err := e.svc.Complete(ctx, state, "c1", "read", "http://x/cb", e.user.ID); !errors.Is(err, strava.ErrScope) {
		t.Errorf("scope: %v", err)
	}
	if l, _ := e.st.StravaLink(ctx, e.rid); l.Connected() {
		t.Error("connected despite refusals")
	}
}
