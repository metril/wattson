package integrations

import (
	"encoding/json"
	"math"

	"github.com/metril/wattson/internal/engine"
)

// Fixed topics. The base is not configurable; only the discovery prefix is.
const (
	baseTopic      = "wattson"
	topicStatus    = baseTopic + "/status"
	topicState     = baseTopic + "/state"
	commandPrefix  = baseTopic + "/command/"
	commandFilter  = commandPrefix + "+"
	payloadOnline  = "online"
	payloadOffline = "offline"
	payloadPress   = "PRESS"
	deviceID       = "wattson"
)

// MQTTState is the JSON payload of the retained wattson/state topic.
type MQTTState struct {
	State       string  `json:"state" doc:"Ride state: idle, connecting, ready, riding, paused or finished."`
	Mode        string  `json:"mode" doc:"Ride mode name, for example free; empty when idle."`
	PowerW      float64 `json:"power_w" doc:"Rider power in watts, rounded to a whole watt."`
	CadenceRpm  float64 `json:"cadence_rpm" doc:"Pedalling cadence in revolutions per minute, rounded to a whole rpm."`
	HRBpm       int     `json:"hr_bpm" doc:"Heart rate in beats per minute; 0 when there is no heart rate source or no reading in the last 5 s."`
	Level       int     `json:"level" doc:"Resistance level (1 to 32) the engine last asked the bike for; 0 when no ride is active."`
	SpeedKmh    float64 `json:"speed_kmh" doc:"Simulated speed in kilometres per hour, to 0.1."`
	GradePct    float64 `json:"grade_pct" doc:"Road grade in percent, to 0.1."`
	DistanceKm  float64 `json:"distance_km" doc:"Simulated distance covered in kilometres, to 0.001."`
	ProgressPct float64 `json:"progress_pct" doc:"Share of the route ridden in percent, to 0.1; 0 when not riding a route."`
	Connected   bool    `json:"connected" doc:"Whether the link to the bike is up right now."`
}

func round(v float64, dp int) float64 {
	p := math.Pow10(dp)
	r := math.Round(v*p) / p
	if r == 0 { // no negative zero in the payload
		return 0
	}
	return r
}

// StateFromTick renders a ride tick as the published state.
func StateFromTick(t engine.Tick) MQTTState {
	s := MQTTState{
		State: string(t.State), Mode: t.Mode,
		PowerW: round(t.PowerW, 0), CadenceRpm: round(t.CadenceRpm, 0), HRBpm: t.HeartRateBpm, Level: t.Level,
		SpeedKmh: round(t.SpeedKmh, 1), GradePct: round(t.GradePct, 1), DistanceKm: round(t.DistanceM/1000, 3),
		Connected: t.Connected,
	}
	if t.RouteLengthM > 0 {
		s.ProgressPct = round(math.Max(0, math.Min(100, t.DistanceM/t.RouteLengthM*100)), 1)
	}
	return s
}

// DiscoveryMsg is one retained Home Assistant discovery config.
type DiscoveryMsg struct {
	Topic   string
	Payload []byte
}

type deviceInfo struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model"`
	SWVersion    string   `json:"sw_version"`
}

type originInfo struct {
	Name      string `json:"name"`
	SWVersion string `json:"sw_version"`
}

type discoveryConfig struct {
	Name              string     `json:"name"`
	UniqueID          string     `json:"unique_id"`
	ObjectID          string     `json:"object_id"`
	AvailabilityTopic string     `json:"availability_topic"`
	StateTopic        string     `json:"state_topic,omitempty"`
	ValueTemplate     string     `json:"value_template,omitempty"`
	Unit              string     `json:"unit_of_measurement,omitempty"`
	DeviceClass       string     `json:"device_class,omitempty"`
	StateClass        string     `json:"state_class,omitempty"`
	Options           []string   `json:"options,omitempty"`
	CommandTopic      string     `json:"command_topic,omitempty"`
	PayloadPress      string     `json:"payload_press,omitempty"`
	Device            deviceInfo `json:"device"`
	Origin            originInfo `json:"origin"`
}

type sensorDef struct{ key, name, unit, class, stateClass, field string }

var sensorDefs = []sensorDef{
	{"power", "Power", "W", "power", "measurement", "power_w"},
	{"cadence", "Cadence", "rpm", "", "measurement", "cadence_rpm"},
	{"heart_rate", "Heart rate", "bpm", "", "measurement", "hr_bpm"},
	{"speed", "Speed", "km/h", "speed", "measurement", "speed_kmh"},
	{"grade", "Grade", "%", "", "measurement", "grade_pct"},
	{"level", "Resistance level", "", "", "measurement", "level"},
	{"distance", "Distance", "km", "distance", "total_increasing", "distance_km"},
	{"progress", "Route progress", "%", "", "measurement", "progress_pct"},
	{"state", "Ride state", "", "enum", "", "state"},
}

type buttonDef struct{ key, name string }

var buttonDefs = []buttonDef{
	{"start", "Start ride"}, {"pause", "Pause"}, {"resume", "Resume"}, {"stop", "Stop ride"},
	{"level_up", "Harder"}, {"level_down", "Easier"}, {"release_bike", "Release bike"},
}

var rideStates = []string{"idle", "connecting", "ready", "riding", "paused", "finished"}

// Discovery returns the retained Home Assistant discovery configs under
// prefix: nine sensors, then seven buttons.
func Discovery(prefix, version string) []DiscoveryMsg {
	dev := deviceInfo{Identifiers: []string{deviceID}, Name: "Wattson", Manufacturer: "Wattson",
		Model: "Wattson trainer controller", SWVersion: version}
	org := originInfo{Name: "Wattson", SWVersion: version}
	out := make([]DiscoveryMsg, 0, len(sensorDefs)+len(buttonDefs))
	add := func(component string, c discoveryConfig) {
		id := "wattson_" + c.UniqueID
		c.UniqueID, c.ObjectID = id, id
		c.AvailabilityTopic = topicStatus
		c.Device, c.Origin = dev, org
		b, _ := json.Marshal(c) // plain strings: cannot fail
		out = append(out, DiscoveryMsg{Topic: prefix + "/" + component + "/" + id + "/config", Payload: b})
	}
	for _, s := range sensorDefs {
		c := discoveryConfig{Name: s.name, UniqueID: s.key, StateTopic: topicState,
			ValueTemplate: "{{ value_json." + s.field + " }}", Unit: s.unit, DeviceClass: s.class, StateClass: s.stateClass}
		if s.class == "enum" {
			c.Options = rideStates
		}
		add("sensor", c)
	}
	for _, b := range buttonDefs {
		add("button", discoveryConfig{Name: b.name, UniqueID: b.key, CommandTopic: commandPrefix + b.key, PayloadPress: payloadPress})
	}
	return out
}
