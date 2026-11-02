package api

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/metril/wattson/internal/esphomeapi"
	"github.com/metril/wattson/internal/esphomeapi/esphometest"
)

func TestHAPing(t *testing.T) {
	t.Parallel()
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/" || r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"message":"API running."}`))
	}))
	t.Cleanup(ha.Close)
	ts := newTS(t)
	admin := ts.setupAdmin(t)

	post := func(body map[string]any) TestResult {
		t.Helper()
		resp, b := ts.do(t, admin, "POST", "/api/settings/test/ha", body)
		if resp.StatusCode != 200 {
			t.Fatalf("test/ha: %d %s", resp.StatusCode, b)
		}
		return decode[TestResult](t, b)
	}
	if r := post(map[string]any{"url": ha.URL, "token": "good"}); !r.OK || r.Message != "Home Assistant API reachable" {
		t.Errorf("good = %+v", r)
	}
	if r := post(map[string]any{"url": ha.URL, "token": "bad"}); r.OK || !contains(r.Message, "401") {
		t.Errorf("bad = %+v", r)
	}
	if r := post(map[string]any{"url": "http://127.0.0.1:1", "token": "good"}); r.OK || r.Message == "" {
		t.Errorf("unreachable = %+v", r)
	}
	if r := post(map[string]any{"token": "good"}); r.OK {
		t.Errorf("no url = %+v", r)
	}
	// Empty fields fall back to the stored values.
	if resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"ha": map[string]any{"url": ha.URL, "token": "good"}}); resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	if r := post(map[string]any{}); !r.OK {
		t.Errorf("stored = %+v", r)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// espDevice starts a fake ESPHome device that answers hello and device info.
func espDevice(t *testing.T, flags uint32) *esphometest.Server {
	t.Helper()
	srv := esphometest.Start(t, make([]byte, 32))
	srv.Handle(esphomeapi.MsgHelloRequest, func([]byte) []esphometest.Reply {
		return []esphometest.Reply{{Type: esphomeapi.MsgHelloResponse,
			Payload: esphomeapi.HelloResponse{APIVersionMajor: 1, APIVersionMinor: 13, Name: esphometest.Name}.Marshal()}}
	})
	srv.Handle(esphomeapi.MsgDeviceInfoRequest, func([]byte) []esphometest.Reply {
		return []esphometest.Reply{{Type: esphomeapi.MsgDeviceInfoResponse,
			Payload: esphomeapi.DeviceInfoResponse{Name: esphometest.Name, MACAddress: esphometest.MAC,
				ESPHomeVersion: "2026.10.1", BTProxyFeatureFlags: flags}.Marshal()}}
	})
	return srv
}

func TestESPHomeTest(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	post := func(body map[string]any) TestResult {
		t.Helper()
		resp, b := ts.do(t, admin, "POST", "/api/settings/test/esphome", body)
		if resp.StatusCode != 200 {
			t.Fatalf("test/esphome: %d %s", resp.StatusCode, b)
		}
		return decode[TestResult](t, b)
	}
	good := espDevice(t, 0x3f)
	if r := post(map[string]any{"host": good.Addr(), "key": validKey()}); !r.OK || !contains(r.Message, esphometest.Name) || !contains(r.Message, "2026.10.1") {
		t.Errorf("good = %+v", r)
	}
	noProxy := espDevice(t, 0)
	if r := post(map[string]any{"host": noProxy.Addr(), "key": validKey()}); r.OK || !contains(r.Message, "no bluetooth_proxy") {
		t.Errorf("no proxy = %+v", r)
	}
	wrong := base64Key(1)
	if r := post(map[string]any{"host": good.Addr(), "key": wrong}); r.OK || r.Message == "" {
		t.Errorf("wrong key = %+v", r)
	}
	if r := post(map[string]any{"host": good.Addr(), "key": "not base64!!"}); r.OK {
		t.Errorf("bad base64 = %+v", r)
	}
	if r := post(map[string]any{}); r.OK {
		t.Errorf("nothing configured = %+v", r)
	}
	// Stored host and key are used when the body omits them.
	if resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"bike": map[string]any{
		"esphome_host": good.Addr(), "esphome_key": validKey()}}); resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	if r := post(map[string]any{}); !r.OK {
		t.Errorf("stored = %+v", r)
	}
}

func base64Key(b byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32))
}
