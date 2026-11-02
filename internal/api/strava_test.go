package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/profile/filedef"

	"github.com/metril/wattson/internal/integrations/strava"
	"github.com/metril/wattson/internal/integrations/strava/stravatest"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
)

const stravaPW = "bob-password-1"

// sharedApp stores the shared Strava app (123/shh) through the settings API.
func (ts *testServer) sharedApp(t testing.TB, admin *http.Client) {
	t.Helper()
	resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"strava": map[string]any{"client_id": "123", "client_secret": "shh"}})
	if resp.StatusCode != 200 {
		t.Fatalf("settings: %d %s", resp.StatusCode, b)
	}
}

func (ts *testServer) stravaLink(t testing.TB, c *http.Client, rider int64) (StravaLinkOut, []byte) {
	t.Helper()
	resp, b := ts.do(t, c, "GET", fmt.Sprintf("/api/riders/%d/strava", rider), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("get strava: %d %s", resp.StatusCode, b)
	}
	return decode[StravaLinkOut](t, b), b
}

// stravaStart begins a connect and walks the fake Strava's authorize step;
// it returns the connect Location and the callback URL Strava redirected to.
func (ts *testServer) stravaStart(t testing.TB, c *http.Client, rider int64, returnTo string) (authURL, callback string) {
	t.Helper()
	path := fmt.Sprintf("/api/riders/%d/strava/connect", rider)
	if returnTo != "" {
		path += "?return_to=" + url.QueryEscape(returnTo)
	}
	resp, b := ts.do(t, c, "GET", path, nil)
	if resp.StatusCode != 302 {
		t.Fatalf("connect: %d %s", resp.StatusCode, b)
	}
	authURL = resp.Header.Get("Location")
	if !strings.HasPrefix(authURL, ts.Fake.Endpoints().AuthURL) {
		t.Fatalf("connect redirected to %q, want Strava", authURL)
	}
	r2, err := ts.client(t).Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	callback = r2.Header.Get("Location")
	if r2.StatusCode != 302 || !strings.HasPrefix(callback, ts.URL+"/api/strava/callback") {
		t.Fatalf("Strava redirected %d to %q", r2.StatusCode, callback)
	}
	return authURL, callback
}

func (ts *testServer) getLoc(t testing.TB, c *http.Client, u string) (int, string) {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Location")
}

// stravaConnectFlow connects rider and returns the final redirect.
func (ts *testServer) stravaConnectFlow(t testing.TB, c *http.Client, rider int64) string {
	t.Helper()
	_, cb := ts.stravaStart(t, c, rider, "")
	code, loc := ts.getLoc(t, c, cb)
	if code != 302 {
		t.Fatalf("callback: %d", code)
	}
	return loc
}

func TestStravaLinkNotConfigured(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	rider := ts.riderOf(t, admin)
	l, _ := ts.stravaLink(t, admin, rider)
	if l.Available || l.Connected || l.UsesOwnApp || l.ClientSecretSet {
		t.Errorf("link = %+v", l)
	}
	if want := ts.URL + "/api/strava/callback"; l.RedirectURI != want || l.CallbackDomain != "127.0.0.1" {
		t.Errorf("redirect_uri %q callback_domain %q", l.RedirectURI, l.CallbackDomain)
	}
	resp, _ := ts.do(t, admin, "GET", fmt.Sprintf("/api/riders/%d/strava/connect", rider), nil)
	if loc := resp.Header.Get("Location"); resp.StatusCode != 302 || loc != "/riders?strava=error&reason=not_configured" {
		t.Errorf("connect = %d %q", resp.StatusCode, loc)
	}
}

func TestStravaConnectFlow(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	ts.sharedApp(t, admin)
	rider := ts.riderOf(t, admin)
	if l, _ := ts.stravaLink(t, admin, rider); !l.Available || l.UsesOwnApp {
		t.Fatalf("link = %+v", l)
	}
	authURL, cb := ts.stravaStart(t, admin, rider, "")
	u, _ := url.Parse(authURL)
	if q := u.Query(); q.Get("client_id") != "123" || q.Get("redirect_uri") != ts.URL+"/api/strava/callback" || q.Get("scope") != "read,activity:write" {
		t.Errorf("authorize query = %v", q)
	}
	if code, loc := ts.getLoc(t, admin, cb); code != 302 || loc != "/riders?strava=connected" {
		t.Fatalf("callback = %d %q", code, loc)
	}
	l, body := ts.stravaLink(t, admin, rider)
	if !l.Connected || l.AthleteName != "Ada Lovelace" || l.AthleteID != 42 || l.Scope != "read,activity:write" || l.ConnectedAt == nil {
		t.Fatalf("link = %+v", l)
	}
	raw, err := ts.Store.StravaLink(context.Background(), rider)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw.RefreshEnc, "v1.") || !strings.HasPrefix(raw.AccessEnc, "v1.") || raw.RefreshEnc == "r1" {
		t.Errorf("tokens are not sealed: access %q refresh %q", raw.AccessEnc, raw.RefreshEnc)
	}
	if got, err := ts.Box.OpenFor(raw.RefreshEnc, fmt.Sprintf("strava.rider.%d.refresh_token", rider)); err != nil || got != "r1" {
		t.Errorf("refresh token opens to %q, %v", got, err)
	}
	if _, err := ts.Box.OpenFor(raw.RefreshEnc, fmt.Sprintf("strava.rider.%d.access_token", rider)); err == nil {
		t.Error("refresh token opens under the access-token AAD")
	}
	if lg := ts.Log.String(); regexp.MustCompile(`shh|\ba1\b|\br1\b`).MatchString(lg) {
		t.Errorf("log contains a secret or token:\n%s", lg)
	}
	for _, s := range []string{"a1", "r1", "shh"} {
		if strings.Contains(string(body), s) {
			t.Errorf("response contains %q", s)
		}
	}
}

func TestStravaConnectReturnTo(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	ts.sharedApp(t, admin)
	rider := ts.riderOf(t, admin)
	_, cb := ts.stravaStart(t, admin, rider, "/settings?tab=strava")
	if _, loc := ts.getLoc(t, admin, cb); loc != "/settings?tab=strava&strava=connected" {
		t.Errorf("loc = %q", loc)
	}
	// An off-site return_to falls back to /.
	_, cb = ts.stravaStart(t, admin, rider, "https://evil.example/x")
	if _, loc := ts.getLoc(t, admin, cb); loc != "/?strava=connected" {
		t.Errorf("loc = %q", loc)
	}
}

func TestStravaCallbackScopeMissing(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	ts.sharedApp(t, admin)
	rider := ts.riderOf(t, admin)
	ts.Fake.GrantScope("read")
	if loc := ts.stravaConnectFlow(t, admin, rider); loc != "/riders?strava=error&reason=scope" {
		t.Errorf("loc = %q", loc)
	}
	if l, _ := ts.stravaLink(t, admin, rider); l.Connected {
		t.Error("connected despite a missing scope")
	}
}

func TestStravaCallbackDenied(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	ts.sharedApp(t, admin)
	rider := ts.riderOf(t, admin)
	ts.Fake.Deny()
	if loc := ts.stravaConnectFlow(t, admin, rider); loc != "/riders?strava=error&reason=denied" {
		t.Errorf("loc = %q", loc)
	}
}

func TestStravaCallbackOtherUser(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	ts.sharedApp(t, admin)
	ts.addUser(t, "bob", stravaPW, false)
	bob := ts.loginAs(t, "bob", stravaPW)
	rider := ts.riderOf(t, admin)
	_, cb := ts.stravaStart(t, admin, rider, "")
	if code, loc := ts.getLoc(t, bob, cb); code != 302 || loc != "/riders?strava=error&reason=state" {
		t.Errorf("bob's replay = %d %q", code, loc)
	}
	if l, _ := ts.stravaLink(t, admin, rider); l.Connected {
		t.Error("admin's link changed")
	}
	// Anonymous callers are sent to the login page, not connected.
	if code, loc := ts.getLoc(t, ts.client(t), cb); code != 302 || loc != "/login" {
		t.Errorf("anonymous = %d %q", code, loc)
	}
}

func TestStravaCallbackTamperedState(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	ts.sharedApp(t, admin)
	rider := ts.riderOf(t, admin)
	_, cb := ts.stravaStart(t, admin, rider, "")
	u, _ := url.Parse(cb)
	q := u.Query()
	st := q.Get("state")
	q.Set("state", st[:len(st)-2]+"AA")
	u.RawQuery = q.Encode()
	if _, loc := ts.getLoc(t, admin, u.String()); loc != "/riders?strava=error&reason=state" {
		t.Errorf("loc = %q", loc)
	}
	q.Del("state")
	u.RawQuery = q.Encode()
	if _, loc := ts.getLoc(t, admin, u.String()); loc != "/riders?strava=error&reason=state" {
		t.Errorf("no state: loc = %q", loc)
	}
}

func TestStravaOwnApp(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	rider := ts.riderOf(t, admin)
	ts.Fake.SetApp("999", "own-sec")
	resp, b := ts.do(t, admin, "PATCH", fmt.Sprintf("/api/riders/%d/strava", rider), map[string]any{"client_id": "999", "client_secret": "own-sec"})
	if resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	l := decode[StravaLinkOut](t, b)
	if !l.UsesOwnApp || !l.ClientSecretSet || !l.Available || l.ClientID != "999" || strings.Contains(string(b), "own-sec") {
		t.Fatalf("link = %s", b)
	}
	if _, body := ts.stravaLink(t, admin, rider); strings.Contains(string(body), "own-sec") {
		t.Error("GET leaks the own secret")
	}
	if resp, b := ts.do(t, admin, "PATCH", fmt.Sprintf("/api/riders/%d/strava", rider), map[string]any{"client_secret": "other"}); resp.StatusCode != 400 {
		t.Errorf("secret without client_id: %d %s, want 400", resp.StatusCode, b)
	}
	authURL, cb := ts.stravaStart(t, admin, rider, "")
	if u, _ := url.Parse(authURL); u.Query().Get("client_id") != "999" {
		t.Errorf("authorize = %s", authURL)
	}
	if _, loc := ts.getLoc(t, admin, cb); loc != "/riders?strava=connected" {
		t.Errorf("loc = %q", loc)
	}
	if lg := ts.Log.String(); regexp.MustCompile(`own-sec|\ba1\b|\br1\b`).MatchString(lg) {
		t.Errorf("log contains a secret or token:\n%s", lg)
	}
}

func TestStravaOwnAppOverridesShared(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	ts.sharedApp(t, admin)
	rider := ts.riderOf(t, admin)
	// An own client ID without a secret is not usable, and never silently uses the shared app.
	ts.do(t, admin, "PATCH", fmt.Sprintf("/api/riders/%d/strava", rider), map[string]any{"client_id": "999"})
	if l, _ := ts.stravaLink(t, admin, rider); l.Available || !l.UsesOwnApp {
		t.Errorf("link = %+v", l)
	}
	// Emptying the client ID returns to the shared app and drops the secret.
	ts.do(t, admin, "PATCH", fmt.Sprintf("/api/riders/%d/strava", rider), map[string]any{"client_id": "999", "client_secret": "x"})
	ts.do(t, admin, "PATCH", fmt.Sprintf("/api/riders/%d/strava", rider), map[string]any{"client_id": ""})
	if l, _ := ts.stravaLink(t, admin, rider); !l.Available || l.UsesOwnApp || l.ClientSecretSet {
		t.Errorf("link = %+v", l)
	}
}

func TestStravaPatchCredsDisconnects(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	ts.sharedApp(t, admin)
	rider := ts.riderOf(t, admin)
	ts.stravaConnectFlow(t, admin, rider)
	// auto_upload alone keeps the connection.
	resp, b := ts.do(t, admin, "PATCH", fmt.Sprintf("/api/riders/%d/strava", rider), map[string]any{"auto_upload": true})
	if l := decode[StravaLinkOut](t, b); resp.StatusCode != 200 || !l.Connected || !l.AutoUpload {
		t.Fatalf("auto_upload patch = %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "PATCH", fmt.Sprintf("/api/riders/%d/strava", rider), map[string]any{"client_id": "998"})
	if l := decode[StravaLinkOut](t, b); resp.StatusCode != 200 || l.Connected || l.ClientID != "998" || !l.AutoUpload {
		t.Fatalf("creds patch = %d %s", resp.StatusCode, b)
	}
	if got := ts.Fake.Deauthorized(); len(got) != 1 || got[0] != "a1" {
		t.Errorf("deauthorized = %v", got)
	}
}

func TestStravaSharedAppChangeDisconnectsSharedRiders(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	ts.sharedApp(t, admin)
	rider := ts.riderOf(t, admin)
	ts.stravaConnectFlow(t, admin, rider)
	// Unrelated settings, and the same credentials again, keep the connection.
	ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"strava": map[string]any{"client_id": "123", "client_secret": "shh"}})
	ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"general": map[string]any{"public_url": "https://w.example"}})
	if l, _ := ts.stravaLink(t, admin, rider); !l.Connected {
		t.Fatal("disconnected without a credential change")
	}
	if resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"strava": map[string]any{"client_secret": "new"}}); resp.StatusCode != 200 {
		t.Fatalf("settings: %d %s", resp.StatusCode, b)
	}
	if l, _ := ts.stravaLink(t, admin, rider); l.Connected {
		t.Error("shared-app rider still connected after the shared secret changed")
	}
	if got := ts.Fake.Deauthorized(); len(got) != 1 || got[0] != "a1" {
		t.Errorf("deauthorized = %v", got)
	}
}

func TestStravaDisconnect(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	ts.sharedApp(t, admin)
	rider := ts.riderOf(t, admin)
	ts.stravaConnectFlow(t, admin, rider)
	if resp, b := ts.do(t, admin, "POST", fmt.Sprintf("/api/riders/%d/strava/disconnect", rider), nil); resp.StatusCode != 204 {
		t.Fatalf("disconnect: %d %s", resp.StatusCode, b)
	}
	if l, _ := ts.stravaLink(t, admin, rider); l.Connected || l.AthleteName != "" || l.ConnectedAt != nil {
		t.Errorf("link = %+v", l)
	}
	if got := ts.Fake.Deauthorized(); len(got) != 1 || got[0] != "a1" {
		t.Errorf("deauthorized = %v", got)
	}
}

func TestStravaDisconnectSurvivesStravaFailure(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	ts.sharedApp(t, admin)
	rider := ts.riderOf(t, admin)
	ts.stravaConnectFlow(t, admin, rider)
	ts.Fake.FailDeauthorize(500)
	if resp, b := ts.do(t, admin, "POST", fmt.Sprintf("/api/riders/%d/strava/disconnect", rider), nil); resp.StatusCode != 204 {
		t.Fatalf("disconnect: %d %s", resp.StatusCode, b)
	}
	if l, _ := ts.stravaLink(t, admin, rider); l.Connected {
		t.Error("still connected")
	}
}

func TestStravaPatchBadClientID(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	rider := ts.riderOf(t, admin)
	for _, id := range []string{"abc", "12a", "1234567890123"} {
		if resp, b := ts.do(t, admin, "PATCH", fmt.Sprintf("/api/riders/%d/strava", rider), map[string]any{"client_id": id}); resp.StatusCode != 400 {
			t.Errorf("client_id %q: %d %s, want 400", id, resp.StatusCode, b)
		}
	}
	resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"strava": map[string]any{"client_id": "abc"}})
	if resp.StatusCode != 400 || !strings.Contains(string(b), "strava.client_id") {
		t.Errorf("settings: %d %s", resp.StatusCode, b)
	}
}

func TestStravaOtherUsersRider404(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	ts.sharedApp(t, admin)
	ts.addUser(t, "bob", stravaPW, false)
	bob := ts.loginAs(t, "bob", stravaPW)
	rider := ts.riderOf(t, admin)
	base := fmt.Sprintf("/api/riders/%d/strava", rider)
	for _, c := range []struct{ method, path string }{
		{"GET", base}, {"PATCH", base}, {"GET", base + "/connect"}, {"POST", base + "/disconnect"},
	} {
		var body any
		if c.method == "PATCH" {
			body = map[string]any{"auto_upload": true}
		}
		if resp, b := ts.do(t, bob, c.method, c.path, body); resp.StatusCode != 404 {
			t.Errorf("%s %s: %d %s, want 404", c.method, c.path, resp.StatusCode, b)
		}
	}
}

func TestStravaKioskTokenCannotConnect(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	rider := ts.riderOf(t, admin)
	tok := ts.mintToken(t, "kiosk", false, "read")
	resp, _ := ts.do(t, nil, "GET", fmt.Sprintf("/api/riders/%d/strava/connect", rider), nil, "Authorization", "Bearer "+tok)
	if resp.StatusCode != 403 {
		t.Errorf("read token connect = %d, want 403", resp.StatusCode)
	}
}

func TestRiderDeleteDeauthorizes(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	ts.sharedApp(t, admin)
	ts.riderOf(t, admin) // the default rider, so the second one may be deleted
	resp, b := ts.do(t, admin, "POST", "/api/riders", map[string]any{"name": "Second"})
	if resp.StatusCode != 201 {
		t.Fatalf("create rider: %d %s", resp.StatusCode, b)
	}
	second := decode[RiderOut](t, b).ID
	ts.stravaConnectFlow(t, admin, second)
	if resp, b := ts.do(t, admin, "DELETE", fmt.Sprintf("/api/riders/%d", second), nil); resp.StatusCode != 204 {
		t.Fatalf("delete: %d %s", resp.StatusCode, b)
	}
	if got := ts.Fake.Deauthorized(); len(got) != 1 || got[0] != "a1" {
		t.Errorf("deauthorized = %v", got)
	}
	if l, err := ts.Store.StravaLink(context.Background(), second); err != nil || l.Connected() || l.ClientID != "" || l.AthleteID != 0 {
		t.Errorf("link after delete = %+v, %v", l, err)
	}
}

func TestStravaCacheControl(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	resp, _ := ts.do(t, admin, "GET", fmt.Sprintf("/api/riders/%d/strava", ts.riderOf(t, admin)), nil)
	if resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("Cache-Control = %q", resp.Header.Get("Cache-Control"))
	}
}

// connectedRide connects the admin's rider to the fake Strava and seeds a
// finished 120-sample route ride.
func (ts *testServer) connectedRide(t testing.TB, admin *http.Client) (rider int64, r store.Ride) {
	t.Helper()
	ts.sharedApp(t, admin)
	rider = ts.riderOf(t, admin)
	ts.stravaConnectFlow(t, admin, rider)
	return rider, ts.seedRide(t, rider, 120, true)
}

func (ts *testServer) rideStrava(t testing.TB, c *http.Client, id int64) StravaStatusOut {
	t.Helper()
	resp, b := ts.do(t, c, "GET", fmt.Sprintf("/api/rides/%d/strava", id), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("get ride strava: %d %s", resp.StatusCode, b)
	}
	return decode[StravaStatusOut](t, b)
}

// settled polls until the ride's upload leaves "uploading".
func (ts *testServer) settled(t testing.TB, c *http.Client, id int64) StravaStatusOut {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := ts.rideStrava(t, c, id)
		if st.Status != "uploading" {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("still uploading: %+v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (ts *testServer) postUpload(t testing.TB, c *http.Client, id int64) (int, StravaStatusOut, []byte) {
	t.Helper()
	resp, b := ts.do(t, c, "POST", fmt.Sprintf("/api/rides/%d/strava", id), nil)
	var out StravaStatusOut
	if resp.StatusCode < 300 {
		out = decode[StravaStatusOut](t, b)
	}
	return resp.StatusCode, out, b
}

func fitRecords(t testing.TB, b []byte) int {
	t.Helper()
	lis := filedef.NewListener()
	defer lis.Close()
	dec := decoder.New(bytes.NewReader(b), decoder.WithMesgListener(lis), decoder.WithBroadcastOnly())
	for dec.Next() {
		if _, err := dec.Decode(); err != nil {
			t.Fatalf("decode fit: %v", err)
		}
	}
	act, ok := lis.File().(*filedef.Activity)
	if !ok {
		t.Fatal("not an activity file")
	}
	return len(act.Records)
}

func TestStravaUploadFlow(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	_, r := ts.connectedRide(t, admin)
	code, st, b := ts.postUpload(t, admin, r.ID)
	if code != 202 || st.Status != "uploading" {
		t.Fatalf("POST = %d %s", code, b)
	}
	st = ts.settled(t, admin, r.ID)
	if st.Status != "done" || st.ActivityID != 777 || st.ActivityURL != "https://www.strava.com/activities/777" || st.UploadID == 0 || st.Error != "" {
		t.Fatalf("status = %+v", st)
	}
	ups := ts.Fake.Uploads()
	if len(ups) != 1 {
		t.Fatalf("uploads = %d", len(ups))
	}
	u := ups[0]
	if u.DataType != "fit" || u.Trainer != "1" || u.Name != "Loop" || u.Description != "Recorded with Wattson" ||
		u.ExternalID != fmt.Sprintf("wattson-%d-%d", r.ID, r.StartedAt.Unix()) {
		t.Errorf("upload = %+v", u)
	}
	if n := fitRecords(t, u.FIT); n != 120 {
		t.Errorf("fit records = %d", n)
	}
	// The ride list carries the state too.
	_, lb := ts.do(t, admin, "GET", "/api/rides", nil)
	if l := decode[RideList](t, lb); len(l.Rides) != 1 || l.Rides[0].Strava.Status != "done" {
		t.Errorf("list = %s", lb)
	}
	if lg := ts.Log.String(); regexp.MustCompile(`shh|\ba1\b|\br1\b`).MatchString(lg) {
		t.Errorf("log contains a secret or token:\n%s", lg)
	}
}

func TestStravaUploadIdempotent(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	_, r := ts.connectedRide(t, admin)
	ts.Fake.ProcessingPolls(5)
	var wg sync.WaitGroup
	codes := make([]int, 10)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i], _, _ = ts.postUpload(t, admin, r.ID)
		}()
	}
	wg.Wait()
	for i, c := range codes {
		if c != 202 && c != 200 {
			t.Errorf("POST %d = %d", i, c)
		}
	}
	if st := ts.settled(t, admin, r.ID); st.Status != "done" {
		t.Fatalf("status = %+v", st)
	}
	if n := len(ts.Fake.Uploads()); n != 1 {
		t.Fatalf("uploads after concurrent POSTs = %d", n)
	}
	if code, st, b := ts.postUpload(t, admin, r.ID); code != 200 || st.Status != "done" {
		t.Errorf("POST after done = %d %s", code, b)
	}
	ride, err := ts.Store.Ride(context.Background(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	ts.Strava.AutoUpload(context.Background(), ride)
	if n := len(ts.Fake.Uploads()); n != 1 {
		t.Errorf("uploads after done = %d", n)
	}
}

func TestStravaUploadDuplicate(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	_, r := ts.connectedRide(t, admin)
	ts.Fake.DuplicateOf(555)
	ts.postUpload(t, admin, r.ID)
	if st := ts.settled(t, admin, r.ID); st.Status != "done" || st.ActivityID != 555 || st.Error != "" {
		t.Errorf("status = %+v", st)
	}
}

func TestStravaUploadError(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	_, r := ts.connectedRide(t, admin)
	ts.Fake.FailNextUpload(200, "Malformed GPS")
	ts.postUpload(t, admin, r.ID)
	if st := ts.settled(t, admin, r.ID); st.Status != "error" || !strings.Contains(st.Error, "Malformed GPS") || st.ActivityID != 0 {
		t.Fatalf("status = %+v", st)
	}
	if code, _, b := ts.postUpload(t, admin, r.ID); code != 202 {
		t.Fatalf("retry = %d %s", code, b)
	}
	if st := ts.settled(t, admin, r.ID); st.Status != "done" || st.Error != "" {
		t.Errorf("after retry = %+v", st)
	}
	if n := len(ts.Fake.Uploads()); n != 2 {
		t.Errorf("uploads = %d", n)
	}
}

func TestStravaUploadRateLimited(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	_, r := ts.connectedRide(t, admin)
	ts.Fake.FailNextUpload(429, "Rate Limit Exceeded")
	ts.postUpload(t, admin, r.ID)
	if st := ts.settled(t, admin, r.ID); st.Status != "error" || !strings.Contains(st.Error, "rate limit") {
		t.Errorf("status = %+v", st)
	}
}

func TestStravaUploadNotConnected(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	ts.sharedApp(t, admin)
	r := ts.seedRide(t, ts.riderOf(t, admin), 120, true)
	code, _, b := ts.postUpload(t, admin, r.ID)
	if code != 409 || errCode(t, b) != "strava_not_connected" {
		t.Fatalf("POST = %d %s", code, b)
	}
	if st := ts.rideStrava(t, admin, r.ID); st.Status != "none" {
		t.Errorf("status = %+v", st)
	}
	if n := len(ts.Fake.Uploads()); n != 0 {
		t.Errorf("uploads = %d", n)
	}
}

func TestStravaUploadInProgressRide(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	rider, _ := ts.connectedRide(t, admin)
	open, err := ts.Store.CreateRide(context.Background(), store.Ride{RiderID: rider, Mode: "free", StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	code, _, b := ts.postUpload(t, admin, open.ID)
	if code != 409 || errCode(t, b) != "ride_in_progress" {
		t.Errorf("POST = %d %s", code, b)
	}
	if resp, b := ts.do(t, admin, "GET", fmt.Sprintf("/api/rides/%d/strava", open.ID), nil); resp.StatusCode != 409 || errCode(t, b) != "ride_in_progress" {
		t.Errorf("GET = %d %s", resp.StatusCode, b)
	}
}

func TestStravaUploadOwnershipAndScope(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	_, r := ts.connectedRide(t, admin)
	ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")
	if code, _, _ := ts.postUpload(t, bob, r.ID); code != 404 {
		t.Errorf("bob POST = %d, want 404", code)
	}
	if resp, _ := ts.do(t, bob, "GET", fmt.Sprintf("/api/rides/%d/strava", r.ID), nil); resp.StatusCode != 404 {
		t.Errorf("bob GET = %d, want 404", resp.StatusCode)
	}
	// A read-scope (kiosk) token cannot start an upload.
	tok := ts.mintToken(t, "kiosk", true, "read")
	if resp, _ := ts.do(t, nil, "POST", fmt.Sprintf("/api/rides/%d/strava", r.ID), nil, bearer(tok)...); resp.StatusCode != 403 {
		t.Errorf("read token POST = %d, want 403", resp.StatusCode)
	}
	if n := len(ts.Fake.Uploads()); n != 0 {
		t.Errorf("uploads = %d", n)
	}
}

func TestRefreshPersisted(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	rider, r := ts.connectedRide(t, admin)
	ctx := context.Background()
	l, err := ts.Store.StravaLink(ctx, rider)
	if err != nil {
		t.Fatal(err)
	}
	l.ExpiresAt = time.Now().Add(-time.Hour)
	if err := ts.Store.PutStravaLink(ctx, l); err != nil {
		t.Fatal(err)
	}
	if code, _, b := ts.postUpload(t, admin, r.ID); code != 202 {
		t.Fatalf("POST = %d %s", code, b)
	}
	if st := ts.settled(t, admin, r.ID); st.Status != "done" {
		t.Fatalf("status = %+v", st)
	}
	if ups := ts.Fake.Uploads(); len(ups) != 1 || ups[0].Bearer != "a2" {
		t.Fatalf("uploads = %+v", ups)
	}
	l, err = ts.Store.StravaLink(ctx, rider)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ts.Box.OpenFor(l.RefreshEnc, fmt.Sprintf("strava.rider.%d.refresh_token", rider)); err != nil || got != "r2" {
		t.Errorf("stored refresh token = %q, %v", got, err)
	}
	if lg := ts.Log.String(); regexp.MustCompile(`shh|\b[ar][12]\b`).MatchString(lg) {
		t.Errorf("log contains a secret or token:\n%s", lg)
	}
}

func TestUploadUnauthorizedClearsLink(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	rider, r := ts.connectedRide(t, admin)
	ts.Fake.Unauthorized(true)
	ts.postUpload(t, admin, r.ID)
	if st := ts.settled(t, admin, r.ID); st.Status != "error" || !strings.Contains(st.Error, "reconnect") {
		t.Fatalf("status = %+v", st)
	}
	if l, _ := ts.stravaLink(t, admin, rider); l.Connected {
		t.Errorf("link still connected: %+v", l)
	}
}

func TestAutoUpload(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	rider, r := ts.connectedRide(t, admin)
	ctx := context.Background()
	ts.Strava.AutoUpload(ctx, r) // auto_upload is off by default
	if st := ts.rideStrava(t, admin, r.ID); st.Status != "none" || len(ts.Fake.Uploads()) != 0 {
		t.Fatalf("auto_upload off: %+v, %d uploads", st, len(ts.Fake.Uploads()))
	}
	if resp, b := ts.do(t, admin, "PATCH", fmt.Sprintf("/api/riders/%d/strava", rider), map[string]any{"auto_upload": true}); resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	ts.Strava.AutoUpload(ctx, r)
	if st := ts.settled(t, admin, r.ID); st.Status != "done" || len(ts.Fake.Uploads()) != 1 {
		t.Errorf("auto_upload on: %+v, %d uploads", st, len(ts.Fake.Uploads()))
	}
}

func TestAutoUploadNotConnected(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	ts.sharedApp(t, admin)
	rider := ts.riderOf(t, admin)
	if resp, b := ts.do(t, admin, "PATCH", fmt.Sprintf("/api/riders/%d/strava", rider), map[string]any{"auto_upload": true}); resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	r := ts.seedRide(t, rider, 120, false)
	ts.Strava.AutoUpload(context.Background(), r)
	if st := ts.rideStrava(t, admin, r.ID); st.Status != "none" || len(ts.Fake.Uploads()) != 0 {
		t.Errorf("status = %+v", st)
	}
}

func TestResumeUploading(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	rider, r := ts.connectedRide(t, admin)
	ctx := context.Background()
	// A previous run uploaded the file and went away mid-processing.
	ts.Fake.ProcessingPolls(2)
	src, err := ts.Strava.Source(ctx, rider)
	if err != nil {
		t.Fatal(err)
	}
	up, err := strava.NewClient(ts.Fake.Endpoints(), nil).Upload(ctx, src, strava.UploadReq{FIT: stravatest.SampleFIT(t), ExternalID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if won, err := ts.Store.ClaimStravaUpload(ctx, r.ID, time.Now()); err != nil || !won {
		t.Fatalf("claim: %v %v", won, err)
	}
	if err := ts.Store.SetStravaState(ctx, r.ID, store.StravaState{Status: "uploading", UploadID: up.ID, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// A second run, with a fresh service on the same store, finishes it.
	svc2 := strava.NewService(strava.ServiceDeps{Store: ts.Store, Box: ts.Box, Settings: ts.Settings, Client: strava.NewClient(ts.Fake.Endpoints(), nil), PollEvery: 10 * time.Millisecond, PollMax: 2 * time.Second})
	t.Cleanup(svc2.Close)
	// A ride that never got its upload ID is reported interrupted.
	lost := ts.seedRide(t, rider, 120, false)
	if won, err := ts.Store.ClaimStravaUpload(ctx, lost.ID, time.Now()); err != nil || !won {
		t.Fatalf("claim: %v %v", won, err)
	}
	svc2.Resume(ctx)
	if st := ts.settled(t, admin, r.ID); st.Status != "done" || st.ActivityID != 777 {
		t.Errorf("resumed = %+v", st)
	}
	if st := ts.settled(t, admin, lost.ID); st.Status != "error" || st.Error != "interrupted; retry" {
		t.Errorf("lost = %+v", st)
	}
	if n := len(ts.Fake.Uploads()); n != 1 {
		t.Errorf("uploads = %d, want the earlier run's only", n)
	}
}

func TestCloseStopsWorkers(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	_, r := ts.connectedRide(t, admin)
	ts.Fake.ProcessingPolls(1000)
	if code, _, b := ts.postUpload(t, admin, r.ID); code != 202 {
		t.Fatalf("POST = %d %s", code, b)
	}
	for deadline := time.Now().Add(5 * time.Second); ts.rideStrava(t, admin, r.ID).UploadID == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("upload id never saved")
		}
	}
	done := make(chan struct{})
	go func() { ts.Strava.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not return within 1 s")
	}
	if st := ts.rideStrava(t, admin, r.ID); st.Status != "uploading" {
		t.Errorf("status after close = %+v, want uploading (resumable)", st)
	}
	code, _, b := ts.postUpload(t, admin, r.ID)
	if code != 503 || errCode(t, b) != "shutting_down" {
		t.Errorf("POST after close = %d %s, want 503 shutting_down", code, b)
	}
	if n := len(ts.Fake.Uploads()); n != 1 {
		t.Errorf("uploads = %d", n)
	}
}

func (ts *testServer) waitUploadID(t testing.TB, c *http.Client, id int64) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ts.rideStrava(t, c, id).UploadID == 0; time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("upload id never saved")
		}
	}
}

func TestResumeResendsAutoUploadRide(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	rider, r := ts.connectedRide(t, admin)
	ctx := context.Background()
	if resp, b := ts.do(t, admin, "PATCH", fmt.Sprintf("/api/riders/%d/strava", rider), map[string]any{"auto_upload": true}); resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	// The last shutdown cut the upload off before it got an upload id.
	if won, err := ts.Store.ClaimStravaUpload(ctx, r.ID, time.Now()); err != nil || !won {
		t.Fatalf("claim: %v %v", won, err)
	}
	ts.Strava.Resume(ctx)
	if st := ts.settled(t, admin, r.ID); st.Status != "done" || st.ActivityID != 777 {
		t.Errorf("status = %+v", st)
	}
	if n := len(ts.Fake.Uploads()); n != 1 {
		t.Errorf("uploads = %d", n)
	}
}

func TestPollSurvivesTransientError(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	_, r := ts.connectedRide(t, admin)
	ts.Fake.ProcessingPolls(30)
	ts.postUpload(t, admin, r.ID)
	ts.waitUploadID(t, admin, r.ID)
	ts.Fake.FailNextUpload(500, "Internal Server Error")
	if st := ts.settled(t, admin, r.ID); st.Status != "done" || st.ActivityID != 777 {
		t.Errorf("status = %+v", st)
	}
	if n := len(ts.Fake.Uploads()); n != 1 {
		t.Errorf("uploads = %d", n)
	}
}

func TestAutoUploadRacesManualUpload(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	rider, r := ts.connectedRide(t, admin)
	if resp, b := ts.do(t, admin, "PATCH", fmt.Sprintf("/api/riders/%d/strava", rider), map[string]any{"auto_upload": true}); resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	ts.Fake.ProcessingPolls(20)
	if code, _, b := ts.postUpload(t, admin, r.ID); code != 202 {
		t.Fatalf("POST = %d %s", code, b)
	}
	ts.Strava.AutoUpload(context.Background(), r) // while the manual upload is in flight
	if st := ts.settled(t, admin, r.ID); st.Status != "done" {
		t.Fatalf("status = %+v", st)
	}
	if n := len(ts.Fake.Uploads()); n != 1 {
		t.Errorf("uploads = %d, want 1", n)
	}
}

func TestUploadNotConfiguredMessage(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withStrava())
	admin := ts.setupAdmin(t)
	rider, r := ts.connectedRide(t, admin)
	ctx := context.Background()
	l, err := ts.Store.StravaLink(ctx, rider)
	if err != nil {
		t.Fatal(err)
	}
	l.ExpiresAt = time.Now().Add(-time.Hour) // forces a refresh, which needs the app
	if err := ts.Store.PutStravaLink(ctx, l); err != nil {
		t.Fatal(err)
	}
	// Cleared behind the API's back: the PATCH handler would disconnect the shared-app riders.
	if _, err := ts.Settings.Update(ctx, func(s *settings.Settings) error {
		s.Strava.ClientID, s.Strava.ClientSecret = "", ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ts.postUpload(t, admin, r.ID)
	if st := ts.settled(t, admin, r.ID); st.Status != "error" || st.Error != "Strava isn't configured on this server — ask an admin" {
		t.Errorf("status = %+v", st)
	}
}
