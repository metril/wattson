package api

import (
	"context"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/metril/wattson/internal/settings"
)

// trustLoopback makes the test server's own address (the peer of every test
// request) a trusted proxy.
func (ts *testServer) trustLoopback(t testing.TB) {
	t.Helper()
	if _, err := ts.Settings.Update(context.Background(), func(s *settings.Settings) error {
		s.Auth.TrustedProxies = []string{"127.0.0.0/8", "::1"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func prefixes(t *testing.T, v ...string) []netip.Prefix {
	t.Helper()
	var out []netip.Prefix
	for _, s := range v {
		p, err := settings.ParseProxy(s)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestClientIP(t *testing.T) {
	t.Parallel()
	proxies := prefixes(t, "10.0.0.0/8", "192.168.1.5", "fd00::/8")
	tests := []struct {
		name    string
		remote  string
		xff     []string
		trusted []netip.Prefix
		want    string
	}{
		{"no proxies configured ignores xff", "10.0.0.1:5000", []string{"1.2.3.4"}, nil, "10.0.0.1"},
		{"untrusted peer ignores xff", "198.51.100.9:5000", []string{"1.2.3.4"}, proxies, "198.51.100.9"},
		{"untrusted peer spoofing a trusted hop", "198.51.100.9:5000", []string{"1.2.3.4, 10.0.0.2"}, proxies, "198.51.100.9"},
		{"trusted peer, single hop", "10.0.0.1:5000", []string{"203.0.113.7"}, proxies, "203.0.113.7"},
		{"trusted peer, no header", "10.0.0.1:5000", nil, proxies, "10.0.0.1"},
		{"spoofed leftmost is skipped", "10.0.0.1:5000", []string{"6.6.6.6, 203.0.113.7"}, proxies, "203.0.113.7"},
		{"rightmost untrusted wins over spoofed chain", "10.0.0.1:5000", []string{"6.6.6.6, 10.9.9.9, 203.0.113.7, 10.0.0.2"}, proxies, "203.0.113.7"},
		{"split across header lines", "10.0.0.1:5000", []string{"6.6.6.6", "203.0.113.7, 10.0.0.2"}, proxies, "203.0.113.7"},
		{"all hops trusted gives leftmost", "10.0.0.1:5000", []string{"10.1.1.1, 10.0.0.2"}, proxies, "10.1.1.1"},
		{"single-IP entry", "192.168.1.5:80", []string{"203.0.113.7"}, proxies, "203.0.113.7"},
		{"garbage hop falls back to the peer", "10.0.0.1:5000", []string{"6.6.6.6, not-an-ip"}, proxies, "10.0.0.1"},
		{"hop with port", "10.0.0.1:5000", []string{"203.0.113.7:4444"}, proxies, "203.0.113.7"},
		{"ipv6 hop", "10.0.0.1:5000", []string{"2001:db8::1"}, proxies, "2001:db8::1"},
		{"bracketed ipv6 hop", "10.0.0.1:5000", []string{"[2001:db8::1]:99"}, proxies, "2001:db8::1"},
		{"ipv6 trusted peer", "[fd00::1]:5000", []string{"203.0.113.7"}, proxies, "203.0.113.7"},
		{"ipv4-mapped peer is trusted", "[::ffff:10.0.0.1]:5000", []string{"203.0.113.7"}, proxies, "203.0.113.7"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remote
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := clientIP(r, tc.trusted); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRequestIsHTTPS(t *testing.T) {
	t.Parallel()
	proxies := prefixes(t, "10.0.0.0/8")
	tests := []struct {
		name   string
		remote string
		proto  []string
		want   bool
	}{
		{"untrusted peer claiming https", "198.51.100.9:1", []string{"https"}, false},
		{"trusted peer https", "10.0.0.1:1", []string{"https"}, true},
		{"trusted peer HTTPS case", "10.0.0.1:1", []string{"HTTPS"}, true},
		{"trusted peer http", "10.0.0.1:1", []string{"http"}, false},
		{"trusted peer, no header", "10.0.0.1:1", nil, false},
		{"nearest proxy wins", "10.0.0.1:1", []string{"https, http"}, false},
		{"nearest proxy wins, https", "10.0.0.1:1", []string{"http, https"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remote
			for _, v := range tc.proto {
				r.Header.Add("X-Forwarded-Proto", v)
			}
			if got := requestIsHTTPS(r, proxies); got != tc.want {
				t.Errorf("requestIsHTTPS = %v, want %v", got, tc.want)
			}
		})
	}
	tlsReq := httptest.NewRequest("GET", "https://x/", nil)
	if !requestIsHTTPS(tlsReq, nil) {
		t.Error("TLS connection must count as https")
	}
}

func TestLoginLimiterKeyedByForwardedClientWhenTrusted(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	ts.trustLoopback(t)
	c := ts.client(t)
	login := func(pw, xff string) int {
		resp, _ := ts.do(t, c, "POST", "/api/auth/login", map[string]any{"username": "bob", "password": pw}, "X-Forwarded-For", xff)
		return resp.StatusCode
	}
	// A spoofed leftmost entry does not buy a fresh key: the rightmost
	// untrusted hop (203.0.113.7) is what counts.
	for i := range 10 {
		if got := login("wrong-password", "6.6.6."+string(rune('0'+i))+", 203.0.113.7"); got != 401 {
			t.Fatalf("attempt %d: %d", i, got)
		}
	}
	if got := login("bob-password-1", "6.6.6.99, 203.0.113.7"); got != 429 {
		t.Errorf("exhausted client behind trusted proxy: %d, want 429", got)
	}
	// A different real client has its own budget.
	if got := login("bob-password-1", "203.0.113.8"); got != 204 {
		t.Errorf("other client: %d, want 204", got)
	}
}

func TestSessionCookieSecureOnlyViaTrustedProxy(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	body := map[string]any{"username": "bob", "password": "bob-password-1"}

	resp, _ := ts.do(t, nil, "POST", "/api/auth/login", body, "X-Forwarded-Proto", "https")
	if ck := sessionCookie(t, resp); ck.Secure {
		t.Errorf("untrusted peer's X-Forwarded-Proto made the cookie Secure: %+v", ck)
	}
	ts.trustLoopback(t)
	resp, _ = ts.do(t, nil, "POST", "/api/auth/login", body, "X-Forwarded-Proto", "https")
	if ck := sessionCookie(t, resp); !ck.Secure {
		t.Errorf("trusted proxy's X-Forwarded-Proto https: cookie not Secure: %+v", ck)
	}
	resp, _ = ts.do(t, nil, "POST", "/api/auth/login", body, "X-Forwarded-Proto", "http")
	if ck := sessionCookie(t, resp); ck.Secure {
		t.Errorf("trusted proxy's http: cookie Secure: %+v", ck)
	}
}

func TestTrustedProxiesSetting(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	_, b := ts.do(t, admin, "GET", "/api/settings", nil)
	if !strings.Contains(string(b), `"trusted_proxies":[]`) {
		t.Errorf("default settings: %s, want trusted_proxies []", b)
	}
	resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"auth": map[string]any{"trusted_proxies": []string{"10.0.0.0/8", "172.18.0.2"}}})
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"trusted_proxies":["10.0.0.0/8","172.18.0.2"]`) {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	// Omitted keeps, [] clears, junk is refused without changing anything.
	resp, b = ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"auth": map[string]any{"session_ttl_hours": 24}})
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"trusted_proxies":["10.0.0.0/8","172.18.0.2"]`) {
		t.Errorf("omitted list not kept: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"auth": map[string]any{"trusted_proxies": []string{"proxy.local"}}})
	if resp.StatusCode != 400 || errCode(t, b) != "invalid_setting" || !strings.Contains(string(b), "auth.trusted_proxies") {
		t.Errorf("junk entry: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"auth": map[string]any{"trusted_proxies": []string{}}})
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"trusted_proxies":[]`) {
		t.Errorf("clear: %d %s", resp.StatusCode, b)
	}
}
