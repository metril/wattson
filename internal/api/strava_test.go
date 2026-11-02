package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
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
