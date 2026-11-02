package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/settings"
)

// SettingsOut is the whole configuration. Secrets are never included: each
// is replaced by a boolean `*_set` that says whether a value is stored.
type SettingsOut struct {
	General   GeneralOut   `json:"general" doc:"General settings."`
	Auth      AuthOut      `json:"auth" doc:"Sign-in settings."`
	Bike      BikeOut      `json:"bike" doc:"Bike connection settings."`
	HA        HAOut        `json:"ha" doc:"Home Assistant connection."`
	MQTT      MQTTOut      `json:"mqtt" doc:"MQTT broker connection."`
	BRouter   BRouterOut   `json:"brouter" doc:"BRouter routing service."`
	Mapillary MapillaryOut `json:"mapillary" doc:"Mapillary street imagery."`
	Strava    StravaOut    `json:"strava" doc:"Strava integration."`
}

type GeneralOut struct {
	PublicURL string `json:"public_url" doc:"External base URL of this server (http or https, no path); empty when unset."`
}

type AuthOut struct {
	AllowLocalLogin bool     `json:"allow_local_login" doc:"Whether username and password sign-in is enabled. The bootstrap admin can always sign in with a password."`
	SessionTTLHours int      `json:"session_ttl_hours" doc:"Session lifetime in hours, 1 to 8760. Applies to sessions created after the change."`
	TrustedProxies  []string `json:"trusted_proxies" doc:"Reverse proxies (CIDRs or single IPs, at most 64) whose X-Forwarded-For and X-Forwarded-Proto headers are trusted for the client address and the https scheme. Empty (the default) ignores those headers."`
}

type BikeOut struct {
	Driver         string `json:"driver" doc:"Bike protocol: auto, fitplus, ftms or sim."`
	Transport      string `json:"transport" doc:"How the bike is reached: local (host Bluetooth) or esphome (Bluetooth proxy)."`
	Address        string `json:"address" doc:"Bluetooth address of the bike, AA:BB:CC:DD:EE:FF; empty when unset."`
	ESPHomeHost    string `json:"esphome_host" doc:"ESPHome Bluetooth proxy as host or host:port (default port 6053); empty when unset."`
	ESPHomeKeySet  bool   `json:"esphome_key_set" doc:"Whether an ESPHome API encryption key is stored. The key itself is never returned."`
	IdleReleaseMin int    `json:"idle_release_min" doc:"Minutes without a command before a bike that is connecting or ready is released, 0 to 240; 0 disables. A bike that is riding or paused is never released; the clock restarts when the ride stops."`
	SimCadenceRpm  int    `json:"sim_cadence_rpm" doc:"Cadence of the simulated bike, in revolutions per minute, 0 to 150."`
}

type HAOut struct {
	URL      string `json:"url" doc:"Home Assistant base URL (no user:pass@); empty when unset."`
	TokenSet bool   `json:"token_set" doc:"Whether a long-lived access token is stored. The token itself is never returned."`
}

type MQTTOut struct {
	URL         string `json:"url" doc:"MQTT broker URL (mqtt:// or mqtts://, no user:pass@); empty when unset."`
	Username    string `json:"username" doc:"MQTT username; empty when unset."`
	PasswordSet bool   `json:"password_set" doc:"Whether an MQTT password is stored. The password itself is never returned."`
}

type BRouterOut struct {
	URL string `json:"url" doc:"BRouter base URL."`
}

type MapillaryOut struct {
	TokenSet bool `json:"token_set" doc:"Whether a Mapillary access token is stored. The token itself is never returned."`
}

type StravaOut struct {
	ClientID        string `json:"client_id" doc:"Strava API client ID; empty when unset."`
	ClientSecretSet bool   `json:"client_secret_set" doc:"Whether a Strava client secret is stored. The secret itself is never returned."`
}

func settingsOut(s settings.Settings) SettingsOut {
	return SettingsOut{
		General: GeneralOut{PublicURL: s.General.PublicURL},
		Auth: AuthOut{AllowLocalLogin: s.Auth.AllowLocalLogin, SessionTTLHours: s.Auth.SessionTTLHours,
			TrustedProxies: append([]string{}, s.Auth.TrustedProxies...)},
		Bike: BikeOut{Driver: s.Bike.Driver, Transport: s.Bike.Transport, Address: s.Bike.Address,
			ESPHomeHost: s.Bike.ESPHomeHost, ESPHomeKeySet: s.Bike.ESPHomeKey != "",
			IdleReleaseMin: s.Bike.IdleReleaseMin, SimCadenceRpm: s.Bike.SimCadenceRpm},
		HA:        HAOut{URL: s.HA.URL, TokenSet: s.HA.Token != ""},
		MQTT:      MQTTOut{URL: s.MQTT.URL, Username: s.MQTT.Username, PasswordSet: s.MQTT.Password != ""},
		BRouter:   BRouterOut{URL: s.BRouter.URL},
		Mapillary: MapillaryOut{TokenSet: s.Mapillary.Token != ""},
		Strava:    StravaOut{ClientID: s.Strava.ClientID, ClientSecretSet: s.Strava.ClientSecret != ""},
	}
}

// SettingsPatch changes some settings. Every section and field is optional;
// an omitted field is left unchanged. For a secret field (esphome_key,
// token, password, client_secret) an omitted value keeps the stored secret,
// an empty string clears it and any other value replaces it.
type SettingsPatch struct {
	General   *GeneralPatch   `json:"general,omitempty" doc:"General settings."`
	Auth      *AuthPatch      `json:"auth,omitempty" doc:"Sign-in settings."`
	Bike      *BikePatch      `json:"bike,omitempty" doc:"Bike connection settings."`
	HA        *HAPatch        `json:"ha,omitempty" doc:"Home Assistant connection."`
	MQTT      *MQTTPatch      `json:"mqtt,omitempty" doc:"MQTT broker connection."`
	BRouter   *BRouterPatch   `json:"brouter,omitempty" doc:"BRouter routing service."`
	Mapillary *MapillaryPatch `json:"mapillary,omitempty" doc:"Mapillary street imagery."`
	Strava    *StravaPatch    `json:"strava,omitempty" doc:"Strava integration."`
}

type GeneralPatch struct {
	PublicURL *string `json:"public_url,omitempty" doc:"External base URL of this server (http or https, no path); empty clears it."`
}

type AuthPatch struct {
	AllowLocalLogin *bool     `json:"allow_local_login,omitempty" doc:"Enable or disable username and password sign-in."`
	SessionTTLHours *int      `json:"session_ttl_hours,omitempty" doc:"Session lifetime in hours, 1 to 8760."`
	TrustedProxies  *[]string `json:"trusted_proxies,omitempty" doc:"Replaces the list of trusted reverse proxies: CIDRs or single IPs, at most 64. An empty list trusts none."`
}

type BikePatch struct {
	Driver         *string `json:"driver,omitempty" doc:"Bike protocol: auto, fitplus, ftms or sim."`
	Transport      *string `json:"transport,omitempty" doc:"local or esphome."`
	Address        *string `json:"address,omitempty" doc:"Bluetooth address AA:BB:CC:DD:EE:FF; empty clears it."`
	ESPHomeHost    *string `json:"esphome_host,omitempty" doc:"ESPHome proxy as host or host:port; empty clears it."`
	ESPHomeKey     *string `json:"esphome_key,omitempty" doc:"ESPHome API encryption key, base64 of 32 bytes. Omit to keep the stored key; an empty string clears it."`
	IdleReleaseMin *int    `json:"idle_release_min,omitempty" doc:"Minutes without a command before a connecting or ready bike is released, 0 to 240; 0 disables."`
	SimCadenceRpm  *int    `json:"sim_cadence_rpm,omitempty" doc:"Simulated cadence in revolutions per minute, 0 to 150."`
}

type HAPatch struct {
	URL   *string `json:"url,omitempty" doc:"Home Assistant base URL (no user:pass@); empty clears it."`
	Token *string `json:"token,omitempty" doc:"Long-lived access token. Omit to keep the stored token; an empty string clears it."`
}

type MQTTPatch struct {
	URL      *string `json:"url,omitempty" doc:"MQTT broker URL (mqtt:// or mqtts://, no user:pass@); empty clears it."`
	Username *string `json:"username,omitempty" doc:"MQTT username."`
	Password *string `json:"password,omitempty" doc:"MQTT password. Omit to keep the stored password; an empty string clears it."`
}

type BRouterPatch struct {
	URL *string `json:"url,omitempty" doc:"BRouter base URL; empty clears it."`
}

type MapillaryPatch struct {
	Token *string `json:"token,omitempty" doc:"Mapillary access token. Omit to keep the stored token; an empty string clears it."`
}

type StravaPatch struct {
	ClientID     *string `json:"client_id,omitempty" doc:"Strava API client ID."`
	ClientSecret *string `json:"client_secret,omitempty" doc:"Strava client secret. Omit to keep the stored secret; an empty string clears it."`
}

func set[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

// apply copies the fields present in p onto s.
func (p SettingsPatch) apply(s *settings.Settings) {
	if g := p.General; g != nil {
		set(&s.General.PublicURL, g.PublicURL)
	}
	if a := p.Auth; a != nil {
		set(&s.Auth.AllowLocalLogin, a.AllowLocalLogin)
		set(&s.Auth.SessionTTLHours, a.SessionTTLHours)
		if a.TrustedProxies != nil {
			s.Auth.TrustedProxies = append([]string{}, *a.TrustedProxies...)
		}
	}
	if b := p.Bike; b != nil {
		set(&s.Bike.Driver, b.Driver)
		set(&s.Bike.Transport, b.Transport)
		set(&s.Bike.Address, b.Address)
		set(&s.Bike.ESPHomeHost, b.ESPHomeHost)
		set(&s.Bike.ESPHomeKey, b.ESPHomeKey)
		set(&s.Bike.IdleReleaseMin, b.IdleReleaseMin)
		set(&s.Bike.SimCadenceRpm, b.SimCadenceRpm)
	}
	if h := p.HA; h != nil {
		set(&s.HA.URL, h.URL)
		set(&s.HA.Token, h.Token)
	}
	if m := p.MQTT; m != nil {
		set(&s.MQTT.URL, m.URL)
		set(&s.MQTT.Username, m.Username)
		set(&s.MQTT.Password, m.Password)
	}
	if b := p.BRouter; b != nil {
		set(&s.BRouter.URL, b.URL)
	}
	if m := p.Mapillary; m != nil {
		set(&s.Mapillary.Token, m.Token)
	}
	if st := p.Strava; st != nil {
		set(&s.Strava.ClientID, st.ClientID)
		set(&s.Strava.ClientSecret, st.ClientSecret)
	}
}

type getSettingsIn struct{}

type settingsBody struct {
	Body SettingsOut
}

type patchSettingsIn struct {
	Body SettingsPatch
}

func (d Deps) registerSettings(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/settings", ID: "getSettings", Tag: "settings",
		Summary: "Get settings",
		Description: "Returns the whole configuration. Secrets are write-only: each is reported as a `*_set` " +
			"boolean (`bike.esphome_key_set`, `ha.token_set`, `mqtt.password_set`, `mapillary.token_set`, " +
			"`strava.client_secret_set`) and the value is never returned.",
		Scope: auth.ScopeAdmin},
		func(ctx context.Context, _ *getSettingsIn) (*settingsBody, error) {
			s, err := d.Settings.Get(ctx)
			if err != nil {
				return nil, err
			}
			return &settingsBody{Body: settingsOut(s)}, nil
		})

	register(api, route{Method: http.MethodPatch, Path: "/api/settings", ID: "updateSettings", Tag: "settings",
		Summary: "Update settings",
		Description: "Changes the fields present in the body and leaves the rest alone. Secret fields are write-only: " +
			"omit one to keep the stored value, send an empty string to clear it, or send a value to replace it. " +
			"The merged result is validated as a whole; a bad value returns 400 `invalid_setting` with the message " +
			"`<section.field>: <problem>` and nothing is stored. Returns the new settings in the same shape as the GET.",
		Scope: auth.ScopeAdmin},
		func(ctx context.Context, in *patchSettingsIn) (*settingsBody, error) {
			s, err := d.Settings.Update(ctx, func(s *settings.Settings) error {
				in.Body.apply(s)
				return nil
			})
			var ve *settings.ValidationError
			if errors.As(err, &ve) {
				return nil, apiErr(http.StatusBadRequest, "invalid_setting", ve.Error())
			}
			if err != nil {
				return nil, err
			}
			return &settingsBody{Body: settingsOut(s)}, nil
		})

	d.registerConnTests(api)
}
