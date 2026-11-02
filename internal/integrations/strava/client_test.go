package strava_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/metril/wattson/internal/integrations/strava"
	"github.com/metril/wattson/internal/integrations/strava/stravatest"
)

var app = strava.App{ClientID: "123", ClientSecret: "shh"}

const redirect = "http://wattson.test/api/strava/callback"

func newClient(t *testing.T) (*strava.Client, *stravatest.Fake) {
	t.Helper()
	f := stravatest.New(t)
	return strava.NewClient(f.Endpoints(), nil), f
}

func staticTS(tok string) oauth2.TokenSource {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: tok, Expiry: time.Now().Add(time.Hour)})
}

func TestAuthCodeURL(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t)
	u, err := url.Parse(c.AuthCodeURL(app, redirect, "s1"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	for k, want := range map[string]string{"client_id": "123", "redirect_uri": redirect, "response_type": "code",
		"scope": "read,activity:write", "approval_prompt": "auto", "state": "s1"} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestExchange(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t)
	g, err := c.Exchange(context.Background(), app, redirect, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Athlete != (strava.Athlete{ID: 42, Name: "Ada Lovelace"}) || g.Scope != "read,activity:write" || g.Token.RefreshToken != "r1" {
		t.Errorf("grant = %+v", g)
	}
	_, err = c.Exchange(context.Background(), strava.App{ClientID: "123", ClientSecret: "wrong-secret"}, redirect, "c1")
	if err == nil {
		t.Fatal("wrong secret: want an error")
	}
	if s := err.Error(); strings.Contains(s, "wrong-secret") || strings.Contains(s, "shh") {
		t.Errorf("error leaks a secret: %v", err)
	}
}

func TestRefreshRotates(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t)
	if _, err := c.Exchange(context.Background(), app, redirect, "c1"); err != nil { // fake now expects r1
		t.Fatal(err)
	}
	tok := &oauth2.Token{AccessToken: "a1", RefreshToken: "r1", Expiry: time.Now().Add(-time.Hour)}
	var got []*oauth2.Token
	ts := c.TokenSource(context.Background(), app, tok, func(n *oauth2.Token) error { got = append(got, n); return nil })
	n, err := ts.Token()
	if err != nil || n.AccessToken != "a2" {
		t.Fatalf("Token = %+v, %v", n, err)
	}
	if len(got) != 1 || got[0].RefreshToken != "r2" {
		t.Fatalf("onRefresh calls = %+v", got)
	}
	if _, err := ts.Token(); err != nil || len(got) != 1 {
		t.Errorf("second Token: err %v, onRefresh calls %d, want no refresh", err, len(got))
	}

	// A persist failure is returned, and retried (not swallowed) next time.
	c2, _ := newClient(t)
	if _, err := c2.Exchange(context.Background(), app, redirect, "c1"); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("disk full")
	calls := 0
	ts = c2.TokenSource(context.Background(), app, tok, func(*oauth2.Token) error { calls++; return boom })
	if _, err := ts.Token(); !errors.Is(err, boom) {
		t.Errorf("Token error = %v, want %v", err, boom)
	}
	if _, err := ts.Token(); !errors.Is(err, boom) || calls != 2 {
		t.Errorf("retry: err %v, calls %d, want the error again and 2 calls", err, calls)
	}
}

func TestRefreshRefusedIsUnauthorized(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t)
	// The fake knows no refresh token yet: it answers 400.
	tok := &oauth2.Token{AccessToken: "a1", RefreshToken: "r9", Expiry: time.Now().Add(-time.Hour)}
	_, err := c.TokenSource(context.Background(), app, tok, nil).Token()
	if !errors.Is(err, strava.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestUploadMultipart(t *testing.T) {
	t.Parallel()
	c, f := newClient(t)
	fit := stravatest.SampleFIT(t)
	u, err := c.Upload(context.Background(), staticTS("a1"), strava.UploadReq{FIT: fit, Filename: "r.fit", Name: "Ramp",
		Description: "d", ExternalID: "wattson-7-1", Trainer: true})
	if err != nil || u.ID <= 0 || u.ActivityID != 0 {
		t.Fatalf("Upload = %+v, %v", u, err)
	}
	got := f.Uploads()
	if len(got) != 1 {
		t.Fatalf("uploads = %d", len(got))
	}
	r := got[0]
	if r.DataType != "fit" || r.Trainer != "1" || r.ExternalID != "wattson-7-1" || r.Name != "Ramp" || r.Bearer != "a1" || string(r.FIT) != string(fit) {
		t.Errorf("received = %+v", r)
	}
}

func TestUploadStatusFlow(t *testing.T) {
	t.Parallel()
	c, f := newClient(t)
	f.ProcessingPolls(2)
	ts := staticTS("a1")
	u, err := c.Upload(context.Background(), ts, strava.UploadReq{FIT: stravatest.SampleFIT(t), Name: "x", ExternalID: "e"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		s, err := c.UploadStatus(context.Background(), ts, u.ID)
		if err != nil || s.ActivityID != 0 || s.Error != "" {
			t.Fatalf("poll %d = %+v, %v", i, s, err)
		}
	}
	s, err := c.UploadStatus(context.Background(), ts, u.ID)
	if err != nil || s.ActivityID != 777 {
		t.Fatalf("final = %+v, %v", s, err)
	}
}

func TestUploadErrors(t *testing.T) {
	t.Parallel()
	c, f := newClient(t)
	ts := staticTS("a1")
	req := strava.UploadReq{FIT: stravatest.SampleFIT(t), Name: "x", ExternalID: "e"}

	f.FailNextUpload(429, "slow down")
	if _, err := c.Upload(context.Background(), ts, req); !errors.Is(err, strava.ErrRateLimited) {
		t.Errorf("429: %v", err)
	}
	f.Unauthorized(true)
	if _, err := c.Upload(context.Background(), ts, req); !errors.Is(err, strava.ErrUnauthorized) {
		t.Errorf("401: %v", err)
	}
	f.Unauthorized(false)
	if _, err := c.Upload(context.Background(), ts, strava.UploadReq{FIT: []byte("not fit"), Name: "x"}); err == nil || errors.Is(err, strava.ErrUnauthorized) {
		t.Errorf("invalid FIT: %v, want a plain error", err)
	}

	f.DuplicateOf(555)
	u, err := c.Upload(context.Background(), ts, req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.UploadStatus(context.Background(), ts, u.ID) // the processing poll
	s, err := c.UploadStatus(context.Background(), ts, u.ID)
	if id, ok := strava.DuplicateActivityID(s.Error); err != nil || !ok || id != 555 {
		t.Errorf("duplicate: %+v, %v", s, err)
	}
}

func TestDuplicateActivityID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		msg  string
		id   int64
		want bool
	}{
		{"a.fit duplicate of activity 123456", 123456, true},
		{"duplicate of <a href='/activities/987'>x</a>", 987, true},
		{"Error processing", 0, false},
		{"duplicate of nothing", 0, false},
	} {
		if id, ok := strava.DuplicateActivityID(tc.msg); id != tc.id || ok != tc.want {
			t.Errorf("%q = %d, %v", tc.msg, id, ok)
		}
	}
}

func TestHasScope(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		granted, want string
		ok            bool
	}{
		{"read,activity:write", "activity:write", true},
		{"read", "activity:write", false},
		{"activity:write_all", "activity:write", false},
		{"", "read", false},
	} {
		if got := strava.HasScope(tc.granted, tc.want); got != tc.ok {
			t.Errorf("HasScope(%q, %q) = %v", tc.granted, tc.want, got)
		}
	}
}

func TestDeauthorize(t *testing.T) {
	t.Parallel()
	c, f := newClient(t)
	if err := c.Deauthorize(context.Background(), "a1"); err != nil {
		t.Fatal(err)
	}
	if got := f.Deauthorized(); len(got) != 1 || got[0] != "a1" {
		t.Errorf("deauthorized = %v", got)
	}
}

func TestResponseSizeCapped(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 2<<20))
	}))
	t.Cleanup(srv.Close)
	c := strava.NewClient(strava.Endpoints{APIBase: srv.URL}, nil)
	if _, err := c.UploadStatus(context.Background(), staticTS("a1"), 1); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("err = %v, want a too-large error", err)
	}
}
