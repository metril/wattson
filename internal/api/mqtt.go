package api

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/integrations"
)

// MQTTBridge is the Home Assistant MQTT bridge (integrations.MQTT).
type MQTTBridge interface {
	// Reload applies changed mqtt settings; it never blocks.
	Reload()
	Status() integrations.MQTTStatus
}

var errNoMQTT = apiErrPublic(http.StatusServiceUnavailable, "no_mqtt", "the MQTT bridge is not available")

type mqttStatusOut struct {
	Body integrations.MQTTStatus
}

type testMQTTIn struct {
	Body struct {
		URL                   string `json:"url,omitempty" doc:"MQTT broker URL to test, mqtt:// or mqtts://. Empty uses the stored URL."`
		Username              string `json:"username,omitempty" doc:"Username to test. Empty uses the stored username, but only when the URL is the stored one."`
		Password              string `json:"password,omitempty" doc:"Password to test. Empty uses the stored password, but only when the URL is the stored one."`
		TLSInsecureSkipVerify *bool  `json:"tls_insecure_skip_verify,omitempty" doc:"Disable certificate checks for an mqtts:// URL (self-signed broker). WARNING: it lets anyone on the path impersonate the broker. Omit to use the stored setting when the URL is the stored one, otherwise checks stay on."`
	}
}

func (d Deps) registerMQTT(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/mqtt/status", ID: "getMQTTStatus", Tag: "settings",
		Summary: "Get the MQTT bridge status",
		Description: "Reports whether an MQTT broker URL is configured, whether the bridge is connected to the broker right now, " +
			"and the latest connection problem (an unreachable broker or a refused login). The bridge keeps retrying by itself; " +
			"saving the `mqtt` settings reconnects it at once.",
		Scope: auth.ScopeAdmin, Errors: []int{http.StatusServiceUnavailable}},
		func(_ context.Context, _ *noIn) (*mqttStatusOut, error) {
			if d.MQTT == nil {
				return nil, errNoMQTT
			}
			return &mqttStatusOut{Body: d.MQTT.Status()}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/settings/test/mqtt", ID: "testMQTT", Tag: "settings",
		Summary: "Test the MQTT broker connection",
		Description: "Makes one MQTT 5 connection with a random client id, timing out after 5 seconds, and disconnects. " +
			"It publishes and subscribes to nothing. Empty body fields fall back to the stored settings, but the stored username and password " +
			"are only used when the URL is the stored one. The result is always HTTP 200; check `ok`. " +
			"A refused login reports the CONNACK reason, for example `not authorized`.",
		Scope: auth.ScopeAdmin},
		func(ctx context.Context, in *testMQTTIn) (*testOut, error) {
			start := time.Now()
			s, err := d.Settings.Get(ctx)
			if err != nil {
				return nil, err
			}
			base := orStored(in.Body.URL, s.MQTT.URL)
			user, pass := in.Body.Username, in.Body.Password
			skip := false
			if in.Body.URL == "" || sameURL(in.Body.URL, s.MQTT.URL) {
				user, pass = orStored(user, s.MQTT.Username), orStored(pass, s.MQTT.Password)
				skip = s.MQTT.TLSInsecureSkipVerify
			}
			if in.Body.TLSInsecureSkipVerify != nil {
				skip = *in.Body.TLSInsecureSkipVerify
			}
			u, err := url.Parse(base)
			switch {
			case base == "" || err != nil || u.Host == "" || (u.Scheme != "mqtt" && u.Scheme != "mqtts"):
				return result(start, false, "MQTT URL is missing or not an absolute mqtt(s) URL"), nil
			case u.User != nil:
				return result(start, false, "the MQTT URL must not contain credentials (user:pass@); use the username and password fields"), nil
			}
			ctx, cancel := context.WithTimeout(ctx, connTestTimeout)
			defer cancel()
			if err := integrations.ProbeMQTT(ctx, base, user, pass, skip); err != nil {
				return result(start, false, "%s", err.Error()), nil
			}
			return result(start, true, "connected to %s (MQTT 5)", serverLabel(base)), nil
		})
}
