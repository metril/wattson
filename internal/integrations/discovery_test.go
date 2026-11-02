package integrations

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"

	"github.com/metril/wattson/internal/engine"
)

var update = flag.Bool("update", false, "regenerate testdata/discovery.golden.json")

func TestDiscoveryGolden(t *testing.T) {
	t.Parallel()
	type entry struct {
		Topic   string          `json:"topic"`
		Payload json.RawMessage `json:"payload"`
	}
	var got []entry
	for _, m := range Discovery("homeassistant", "1.2.3") {
		got = append(got, entry{m.Topic, m.Payload})
	}
	if len(got) != 16 {
		t.Fatalf("%d discovery messages, want 16", len(got))
	}
	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	const path = "testdata/discovery.golden.json"
	if *update {
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, want) {
		t.Errorf("discovery differs from %s; run: go test ./internal/integrations -run TestDiscoveryGolden -update", path)
	}
}

func TestDiscoveryPower(t *testing.T) {
	t.Parallel()
	var msg *DiscoveryMsg
	for _, m := range Discovery("homeassistant", "1.2.3") {
		if m.Topic == "homeassistant/sensor/wattson_power/config" {
			msg = &m
		}
	}
	if msg == nil {
		t.Fatal("no power sensor config")
	}
	var c map[string]any
	if err := json.Unmarshal(msg.Payload, &c); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"name": "Power", "unique_id": "wattson_power", "default_entity_id": "sensor.wattson_power", "unit_of_measurement": "W",
		"device_class": "power", "state_class": "measurement", "state_topic": "wattson/state",
		"availability_topic": "wattson/status", "value_template": "{{ value_json.power_w }}",
	} {
		if c[k] != want {
			t.Errorf("%s = %v, want %q", k, c[k], want)
		}
	}
	dev := c["device"].(map[string]any)
	if ids := dev["identifiers"].([]any); len(ids) != 1 || ids[0] != "wattson" || dev["sw_version"] != "1.2.3" {
		t.Errorf("device = %v", dev)
	}
}

func TestDiscoveryButtonsAndEnum(t *testing.T) {
	t.Parallel()
	seen := map[string]map[string]any{}
	for _, m := range Discovery("p", "v") {
		var c map[string]any
		if err := json.Unmarshal(m.Payload, &c); err != nil {
			t.Fatal(err)
		}
		seen[m.Topic] = c
	}
	b := seen["p/button/wattson_level_up/config"]
	if b["command_topic"] != "wattson/command/level_up" || b["payload_press"] != "PRESS" || b["name"] != "Harder" {
		t.Errorf("level_up = %v", b)
	}
	if seen["p/button/wattson_release_bike/config"] == nil {
		t.Error("no release_bike button")
	}
	if opts := seen["p/sensor/wattson_state/config"]["options"].([]any); len(opts) != 6 {
		t.Errorf("state options = %v", opts)
	}
}

func TestStateFromTick(t *testing.T) {
	t.Parallel()
	got := StateFromTick(engine.Tick{State: engine.StateRiding, PowerW: 201.6, DistanceM: 1234.4, RouteLengthM: 2468.8, HeartRateBpm: 72})
	if got.PowerW != 202 || got.DistanceKm != 1.234 || got.ProgressPct != 50 || got.HRBpm != 72 || got.State != "riding" {
		t.Errorf("got %+v", got)
	}
	if p := StateFromTick(engine.Tick{DistanceM: 500}).ProgressPct; p != 0 {
		t.Errorf("progress without a route = %v", p)
	}
	g := StateFromTick(engine.Tick{SpeedKmh: 24.96, GradePct: -0.04, CadenceRpm: 80.4})
	if g.SpeedKmh != 25 || g.GradePct != 0 || g.CadenceRpm != 80 {
		t.Errorf("rounding: %+v", g)
	}
}
