package api

import (
	"encoding/base64"
	"strings"
	"testing"
)

type settingsView struct {
	Auth struct {
		AllowLocalLogin bool `json:"allow_local_login"`
		SessionTTLHours int  `json:"session_ttl_hours"`
	} `json:"auth"`
	Bike struct {
		Driver        string `json:"driver"`
		ESPHomeKeySet bool   `json:"esphome_key_set"`
	} `json:"bike"`
	HA struct {
		TokenSet bool `json:"token_set"`
	} `json:"ha"`
	MQTT struct {
		PasswordSet bool `json:"password_set"`
	} `json:"mqtt"`
	Mapillary struct {
		TokenSet bool `json:"token_set"`
	} `json:"mapillary"`
	Strava struct {
		ClientSecretSet bool `json:"client_secret_set"`
	} `json:"strava"`
}

func validKey() string { return base64.StdEncoding.EncodeToString(make([]byte, 32)) }

func TestSettingsGetDefaults(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	resp, b := ts.do(t, admin, "GET", "/api/settings", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("get: %d %s", resp.StatusCode, b)
	}
	s := decode[settingsView](t, b)
	if s.Bike.Driver != "sim" || !s.Auth.AllowLocalLogin || s.Auth.SessionTTLHours != 168 || s.HA.TokenSet {
		t.Errorf("defaults = %s", b)
	}
}

func TestSettingsNeverReturnSecrets(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	key := validKey()
	secrets := []string{"tok-plain-123", "mq-plain-456", "st-plain-789", key, "map-plain-000"}
	resp, pb := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{
		"ha":        map[string]any{"token": secrets[0]},
		"mqtt":      map[string]any{"password": secrets[1]},
		"strava":    map[string]any{"client_secret": secrets[2]},
		"bike":      map[string]any{"esphome_key": key},
		"mapillary": map[string]any{"token": secrets[4]},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, pb)
	}
	_, gb := ts.do(t, admin, "GET", "/api/settings", nil)
	for _, body := range [][]byte{pb, gb} {
		for _, sec := range secrets {
			if strings.Contains(string(body), sec) {
				t.Errorf("body leaks %q: %s", sec, body)
			}
		}
		s := decode[settingsView](t, body)
		if !s.HA.TokenSet || !s.MQTT.PasswordSet || !s.Strava.ClientSecretSet || !s.Bike.ESPHomeKeySet || !s.Mapillary.TokenSet {
			t.Errorf("*_set flags not all true: %s", body)
		}
	}
	if strings.Contains(ts.Log.String(), secrets[0]) {
		t.Error("log leaks a secret")
	}

	// Omitted secret = keep.
	resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"ha": map[string]any{"url": "http://ha.local:8123"}})
	if resp.StatusCode != 200 || !decode[settingsView](t, b).HA.TokenSet {
		t.Errorf("omitted token not kept: %d %s", resp.StatusCode, b)
	}
	got, err := ts.Settings.Get(t.Context())
	if err != nil || got.HA.Token != secrets[0] {
		t.Errorf("stored token = %q, %v", got.HA.Token, err)
	}
	// Empty string = clear.
	resp, b = ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"ha": map[string]any{"token": ""}})
	if resp.StatusCode != 200 || decode[settingsView](t, b).HA.TokenSet {
		t.Errorf("empty token not cleared: %d %s", resp.StatusCode, b)
	}
	got, _ = ts.Settings.Get(t.Context())
	if got.HA.Token != "" || got.HA.URL != "http://ha.local:8123" {
		t.Errorf("after clear = %+v", got.HA)
	}
}

func TestSettingsValidation(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"auth": map[string]any{"session_ttl_hours": 0}})
	if resp.StatusCode != 400 || errCode(t, b) != "invalid_setting" {
		t.Fatalf("ttl 0: %d %s", resp.StatusCode, b)
	}
	if msg := decode[ErrorResponse](t, b).Error.Message; !strings.HasPrefix(msg, "auth.session_ttl_hours") {
		t.Errorf("message = %q", msg)
	}
	// Nothing was stored.
	_, b = ts.do(t, admin, "GET", "/api/settings", nil)
	if decode[settingsView](t, b).Auth.SessionTTLHours != 168 {
		t.Errorf("invalid patch changed settings: %s", b)
	}
}

func TestSettingsAdminOnly(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")
	for _, tc := range [][3]string{
		{"GET", "/api/settings", ""}, {"PATCH", "/api/settings", "{}"},
		{"POST", "/api/settings/test/ha", "{}"}, {"POST", "/api/settings/test/esphome", "{}"},
	} {
		var body any
		if tc[2] != "" {
			body = map[string]any{}
		}
		if resp, b := ts.do(t, bob, tc[0], tc[1], body); resp.StatusCode != 403 {
			t.Errorf("%s %s: %d %s, want 403", tc[0], tc[1], resp.StatusCode, b)
		}
	}
}

func TestSessionTTLApplies(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	if resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"auth": map[string]any{"session_ttl_hours": 1}}); resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	resp, b := ts.do(t, ts.client(t), "POST", "/api/auth/login", map[string]any{"username": "admin", "password": testPW})
	if resp.StatusCode != 204 {
		t.Fatalf("login: %d %s", resp.StatusCode, b)
	}
	if got := resp.Header.Get("Set-Cookie"); !strings.Contains(got, "Max-Age=3600") {
		t.Errorf("Set-Cookie = %q, want Max-Age=3600", got)
	}
}
