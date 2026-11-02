package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/metril/wattson/internal/hass"
	"github.com/metril/wattson/internal/hass/hasstest"
)

func haFake(t *testing.T) *hasstest.Server {
	t.Helper()
	s := hasstest.Start(t, "good-token-123")
	s.SetState("sensor.p", "150", map[string]any{"friendly_name": "Bike power", "unit_of_measurement": "W", "device_class": "power"})
	s.SetState("number.r", "3", map[string]any{"friendly_name": "Resistance", "min": 1.0, "max": 24.0})
	s.SetState("sensor.c", "80", nil)
	s.SetState("light.x", "on", nil)
	return s
}

func TestHAEntities(t *testing.T) {
	t.Parallel()
	ha := haFake(t)
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	if resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"ha": map[string]any{"url": ha.URL(), "token": "good-token-123"}}); resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	list := func(q string) HAEntities {
		t.Helper()
		resp, b := ts.do(t, admin, "GET", "/api/ha/entities?"+q, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d %s", q, resp.StatusCode, b)
		}
		return decode[HAEntities](t, b)
	}
	if got := list("domain=number"); len(got.Entities) != 1 || got.Entities[0] != (HAEntity{EntityID: "number.r", Name: "Resistance", State: "3"}) || got.Truncated {
		t.Errorf("number = %+v", got)
	}
	got := list("domain=sensor")
	if len(got.Entities) != 2 || got.Entities[0].EntityID != "sensor.c" || got.Entities[1].EntityID != "sensor.p" {
		t.Fatalf("sensor = %+v", got)
	}
	if c := got.Entities[0]; c.Name != "sensor.c" {
		t.Errorf("a missing friendly name falls back to the id: %+v", c)
	}
	if p := got.Entities[1]; p != (HAEntity{EntityID: "sensor.p", Name: "Bike power", State: "150", Unit: "W", DeviceClass: "power"}) {
		t.Errorf("sensor.p = %+v", p)
	}
	if got := list("domain=sensor&device_class=power"); len(got.Entities) != 1 || got.Entities[0].EntityID != "sensor.p" {
		t.Errorf("device_class = %+v", got)
	}
	for _, q := range []string{"", "domain=light", "domain="} {
		if resp, b := ts.do(t, admin, "GET", "/api/ha/entities?"+q, nil); resp.StatusCode != 400 {
			t.Errorf("%q: %d %s, want 400", q, resp.StatusCode, b)
		}
	}
}

func TestPickEntitiesCaps(t *testing.T) {
	t.Parallel()
	got := pickEntities(manyStates(haEntityCap+5), "sensor", "")
	if len(got.Entities) != haEntityCap || !got.Truncated || got.Entities[0].EntityID != "sensor.e0000" {
		t.Fatalf("len %d truncated %v first %q", len(got.Entities), got.Truncated, got.Entities[0].EntityID)
	}
}

func manyStates(n int) []hass.State {
	out := make([]hass.State, n)
	for i := range out {
		out[i] = hass.State{EntityID: fmt.Sprintf("sensor.e%04d", n-1-i), State: "1"}
	}
	return out
}

func TestHAEntitiesNotConfigured(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	resp, b := ts.do(t, admin, "GET", "/api/ha/entities?domain=number", nil)
	if resp.StatusCode != 400 || !contains(string(b), `"ha_not_configured"`) {
		t.Fatalf("%d %s, want 400 ha_not_configured", resp.StatusCode, b)
	}
}

func TestHAEntitiesBadToken(t *testing.T) {
	t.Parallel()
	ha := haFake(t)
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	if resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"ha": map[string]any{"url": ha.URL(), "token": "wrong-token-123"}}); resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	resp, b := ts.do(t, admin, "GET", "/api/ha/entities?domain=number", nil)
	if resp.StatusCode != 502 || !contains(string(b), `"ha_unavailable"`) || !contains(strings.ToLower(string(b)), "token") {
		t.Fatalf("%d %s, want 502 ha_unavailable mentioning the token", resp.StatusCode, b)
	}
	if contains(string(b), "wrong-token-123") {
		t.Fatalf("the token leaked: %s", b)
	}
}

func TestHAEntitiesUnreachable(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	if resp, b := ts.do(t, admin, "PATCH", "/api/settings", map[string]any{"ha": map[string]any{"url": "http://127.0.0.1:1", "token": "some-token-123"}}); resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	resp, b := ts.do(t, admin, "GET", "/api/ha/entities?domain=sensor", nil)
	if resp.StatusCode != 502 || !contains(string(b), `"ha_unavailable"`) || contains(string(b), "some-token-123") {
		t.Fatalf("%d %s, want 502 ha_unavailable", resp.StatusCode, b)
	}
}
