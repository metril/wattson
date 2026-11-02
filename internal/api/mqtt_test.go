package api

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/metril/wattson/internal/integrations"
	"github.com/metril/wattson/internal/integrations/mqtttest"
)

type fakeMQTT struct {
	reloads atomic.Int32
	status  integrations.MQTTStatus
}

func (f *fakeMQTT) Reload()                         { f.reloads.Add(1) }
func (f *fakeMQTT) Status() integrations.MQTTStatus { return f.status }

func TestMQTTTestEndpoint(t *testing.T) {
	t.Parallel()
	b := mqtttest.Start(t, map[string]string{"u": "p"})
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	post := func(body map[string]any) TestResult {
		t.Helper()
		resp, raw := ts.do(t, admin, "POST", "/api/settings/test/mqtt", body)
		if resp.StatusCode != 200 {
			t.Fatalf("test/mqtt: %d %s", resp.StatusCode, raw)
		}
		return decode[TestResult](t, raw)
	}
	if r := post(map[string]any{"url": b.URL(), "username": "u", "password": "p"}); !r.OK || !strings.HasPrefix(r.Message, "connected to ") {
		t.Errorf("good = %+v", r)
	}
	r := post(map[string]any{"url": b.URL(), "username": "u", "password": "wrong"})
	if r.OK || !strings.Contains(r.Message, "refused") || strings.Contains(r.Message, "wrong") {
		t.Errorf("wrong password = %+v", r)
	}
	if r := post(map[string]any{"url": "mqtt://127.0.0.1:1"}); r.OK || r.Message == "" {
		t.Errorf("closed port = %+v", r)
	}
	if r := post(map[string]any{"url": "http://x"}); r.OK {
		t.Errorf("http url = %+v", r)
	}
	if r := post(map[string]any{}); r.OK {
		t.Errorf("no url = %+v", r)
	}
	if r := post(map[string]any{"url": strings.Replace(b.URL(), "mqtt://", "mqtt://u:p@", 1)}); r.OK || strings.Contains(r.Message, "p@") {
		t.Errorf("userinfo url = %+v", r)
	}
	// Empty fields fall back to the stored values.
	if resp, raw := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"mqtt": map[string]any{"url": b.URL(), "username": "u", "password": "p"}}); resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, raw)
	}
	if r := post(map[string]any{}); !r.OK {
		t.Errorf("stored = %+v", r)
	}
	// Stored credentials are not sent to another URL.
	other := mqtttest.Start(t, map[string]string{"u": "p"})
	if r := post(map[string]any{"url": other.URL()}); r.OK {
		t.Errorf("stored credentials reached another broker: %+v", r)
	}
}

func TestSettingsPatchReloadsMQTT(t *testing.T) {
	t.Parallel()
	f := &fakeMQTT{}
	ts := newTS(t, withMQTT(f))
	admin := ts.setupAdmin(t)
	patch := func(body map[string]any) {
		t.Helper()
		if resp, raw := ts.do(t, admin, "PATCH", "/api/settings", body); resp.StatusCode != 200 {
			t.Fatalf("patch: %d %s", resp.StatusCode, raw)
		}
	}
	patch(map[string]any{"general": map[string]any{"public_url": "https://example.com"}})
	if n := f.reloads.Load(); n != 0 {
		t.Errorf("reloads after a general change = %d, want 0", n)
	}
	patch(map[string]any{"mqtt": map[string]any{"url": "mqtt://broker:1883"}})
	if n := f.reloads.Load(); n != 1 {
		t.Errorf("reloads after mqtt.url = %d, want 1", n)
	}
	patch(map[string]any{"mqtt": map[string]any{"url": "mqtt://broker:1883"}}) // unchanged
	if n := f.reloads.Load(); n != 1 {
		t.Errorf("reloads after an unchanged mqtt section = %d, want 1", n)
	}
}

func TestMQTTStatusEndpoint(t *testing.T) {
	t.Parallel()
	f := &fakeMQTT{status: integrations.MQTTStatus{Enabled: true, Error: "the broker refused the connection"}}
	ts := newTS(t, withMQTT(f))
	admin := ts.setupAdmin(t)
	resp, raw := ts.do(t, admin, "GET", "/api/mqtt/status", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d %s", resp.StatusCode, raw)
	}
	if got := decode[integrations.MQTTStatus](t, raw); got != f.status {
		t.Errorf("status = %+v, want %+v", got, f.status)
	}
	bare := newTS(t)
	if resp, _ := bare.do(t, bare.setupAdmin(t), "GET", "/api/mqtt/status", nil); resp.StatusCode != 503 {
		t.Errorf("without a bridge: %d, want 503", resp.StatusCode)
	}
}

func TestMQTTTLSInsecureSetting(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	_, raw := ts.do(t, admin, "GET", "/api/settings", nil)
	if decode[struct {
		MQTT struct {
			Insecure bool `json:"tls_insecure_skip_verify"`
		} `json:"mqtt"`
	}](t, raw).MQTT.Insecure {
		t.Fatal("tls_insecure_skip_verify should default to false")
	}
	resp, raw := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"mqtt": map[string]any{"tls_insecure_skip_verify": true}})
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"tls_insecure_skip_verify":true`) {
		t.Errorf("patch: %d %s", resp.StatusCode, raw)
	}
}
