package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/blue/bluetest"
	"github.com/metril/wattson/internal/ridectl"
)

func TestDiagnosticsAdminOnly(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	ts.setupAdmin(t)
	ctl := ts.mintToken(t, "driver", false, "control")
	for _, path := range []string{"/api/diagnostics/bluetooth", "/api/ha/entities?domain=number"} {
		if resp, b := ts.do(t, nil, "GET", path, nil, bearer(ctl)...); resp.StatusCode != 403 {
			t.Errorf("%s: %d %s, want 403", path, resp.StatusCode, b)
		}
		if resp, b := ts.do(t, nil, "GET", path, nil); resp.StatusCode != 401 {
			t.Errorf("%s anonymous: %d %s, want 401", path, resp.StatusCode, b)
		}
	}
}

func TestDiagnosticsEndpoint(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withOpenLocal(func() (blue.Transport, error) { return bluetest.NewFake(), nil }))
	admin := ts.setupAdmin(t)

	// The simulator uses no Bluetooth.
	resp, b := ts.do(t, admin, "GET", "/api/diagnostics/bluetooth", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("sim: %d %s", resp.StatusCode, b)
	}
	if d := decode[ridectl.Diagnostics](t, b); d.Transport != "none" || d.Devices == nil {
		t.Fatalf("sim diagnostics = %s", b)
	}

	if resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"bike": map[string]any{
		"driver": "fitplus", "address": "C0:FF:EE:00:00:01/random"}}); resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "GET", "/api/diagnostics/bluetooth?scan_s=1", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("diagnostics: %d %s", resp.StatusCode, b)
	}
	d := decode[ridectl.Diagnostics](t, b)
	if d.Transport != "local" || d.Local == nil || d.Local.Status != "ok" || len(d.Devices) != 1 || d.Devices[0].Role != "bike" || d.Devices[0].State != "idle" {
		t.Fatalf("diagnostics = %s", b)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(b, &raw)
	if _, ok := raw["proxy"]; ok {
		t.Errorf("a local check carries a proxy part: %s", b)
	}

	for _, q := range []string{"scan_s=11", "scan_s=-1", "scan_s=x"} {
		if resp, b := ts.do(t, admin, "GET", "/api/diagnostics/bluetooth?"+q, nil); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", q, resp.StatusCode, b)
		}
	}
}

func TestDiagnosticsNoBikeManager(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withoutBike())
	admin := ts.setupAdmin(t)
	if resp, b := ts.do(t, admin, "GET", "/api/diagnostics/bluetooth", nil); resp.StatusCode != 503 {
		t.Fatalf("%d %s, want 503", resp.StatusCode, b)
	}
}
