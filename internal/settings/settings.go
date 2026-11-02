// Package settings holds the typed, validated runtime configuration that
// lives in SQLite. Secret fields are sealed at rest by Service.
package settings

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"

	"github.com/metril/wattson/internal/blue"
)

// Settings is the whole configuration, one struct per stored section.
type Settings struct {
	General   General   `json:"general"`
	Auth      Auth      `json:"auth"`
	Bike      Bike      `json:"bike"`
	HA        HA        `json:"ha"`
	MQTT      MQTT      `json:"mqtt"`
	BRouter   BRouter   `json:"brouter"`
	Mapillary Mapillary `json:"mapillary"`
	Strava    Strava    `json:"strava"`
}

type General struct {
	PublicURL string `json:"public_url"`
}

type Auth struct {
	AllowLocalLogin bool `json:"allow_local_login"`
	SessionTTLHours int  `json:"session_ttl_hours"`
	// TrustedProxies are the CIDRs (or bare IPs) of reverse proxies whose
	// X-Forwarded-For and X-Forwarded-Proto headers are believed. Empty means
	// those headers are ignored.
	TrustedProxies []string `json:"trusted_proxies"`
}

// MaxTrustedProxies bounds auth.trusted_proxies.
const MaxTrustedProxies = 64

// ParseProxy parses one auth.trusted_proxies entry: a CIDR such as
// 10.0.0.0/8 or a bare IP address (a /32 or /128). The result is masked.
func ParseProxy(v string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(v); err == nil {
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(v)
	if err != nil || a.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("%q is not an IP address or CIDR", v)
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

type Bike struct {
	Driver         string `json:"driver"`
	Transport      string `json:"transport"`
	Address        string `json:"address"`
	ESPHomeHost    string `json:"esphome_host"`
	ESPHomeKey     string `json:"esphome_key"` // secret
	IdleReleaseMin int    `json:"idle_release_min"`
	SimCadenceRpm  int    `json:"sim_cadence_rpm"`
}

type HA struct {
	URL   string `json:"url"`
	Token string `json:"token"` // secret
}

type MQTT struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"` // secret
}

type BRouter struct {
	URL string `json:"url"`
}

type Mapillary struct {
	Token string `json:"token"` // secret
}

type Strava struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"` // secret
}

// Defaults returns the settings used before anything is stored.
func Defaults() Settings {
	return Settings{
		Auth:    Auth{AllowLocalLogin: true, SessionTTLHours: 168},
		Bike:    Bike{Driver: "sim", Transport: "local", IdleReleaseMin: 10, SimCadenceRpm: 80},
		BRouter: BRouter{URL: "http://brouter:17777"},
	}
}

// ValidationError names the offending field as section.json_field.
type ValidationError struct{ Field, Msg string }

func (e *ValidationError) Error() string { return e.Field + ": " + e.Msg }

func bad(field, format string, a ...any) error {
	return &ValidationError{Field: field, Msg: fmt.Sprintf(format, a...)}
}

// Validate returns a *ValidationError for the first invalid field.
func (s Settings) Validate() error {
	if v := s.General.PublicURL; v != "" {
		u, ok := parseURL(v, "http", "https")
		if !ok || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return bad("general.public_url", "must be an absolute http(s) URL without a path")
		}
	}
	if v := s.Auth.SessionTTLHours; v < 1 || v > 8760 {
		return bad("auth.session_ttl_hours", "must be between 1 and 8760")
	}
	if len(s.Auth.TrustedProxies) > MaxTrustedProxies {
		return bad("auth.trusted_proxies", "at most %d entries", MaxTrustedProxies)
	}
	for _, v := range s.Auth.TrustedProxies {
		if _, err := ParseProxy(v); err != nil {
			return bad("auth.trusted_proxies", "%v (use a CIDR like 10.0.0.0/8 or a single IP)", err)
		}
	}
	switch s.Bike.Driver {
	case "auto", "fitplus", "ftms", "sim":
	default:
		return bad("bike.driver", "must be one of auto, fitplus, ftms, sim")
	}
	switch s.Bike.Transport {
	case "local", "esphome":
	default:
		return bad("bike.transport", "must be local or esphome")
	}
	if v := s.Bike.Address; v != "" {
		if _, err := blue.ParseAddress(v); err != nil {
			return bad("bike.address", "must be a Bluetooth address like AA:BB:CC:DD:EE:FF")
		}
	}
	if v := s.Bike.ESPHomeHost; v != "" && !validHostPort(v) {
		return bad("bike.esphome_host", "must be host or host:port")
	}
	if v := s.Bike.ESPHomeKey; v != "" {
		if k, err := base64.StdEncoding.DecodeString(v); err != nil || len(k) != 32 {
			return bad("bike.esphome_key", "must be base64 of 32 bytes")
		}
	}
	if v := s.Bike.IdleReleaseMin; v < 0 || v > 240 {
		return bad("bike.idle_release_min", "must be between 0 and 240")
	}
	if v := s.Bike.SimCadenceRpm; v < 0 || v > 150 {
		return bad("bike.sim_cadence_rpm", "must be between 0 and 150")
	}
	if v := s.HA.URL; v != "" {
		if u, ok := parseURL(v, "http", "https"); !ok {
			return bad("ha.url", "must be an absolute http(s) URL")
		} else if u.User != nil {
			return bad("ha.url", "must not contain credentials (user:pass@); use the token field")
		}
	}
	if v := s.MQTT.URL; v != "" {
		if u, ok := parseURL(v, "mqtt", "mqtts"); !ok {
			return bad("mqtt.url", "must be an absolute mqtt(s) URL")
		} else if u.User != nil {
			return bad("mqtt.url", "must not contain credentials (user:pass@); use the username and password fields")
		}
	}
	if v := s.BRouter.URL; v != "" {
		if _, ok := parseURL(v, "http", "https"); !ok {
			return bad("brouter.url", "must be an absolute http(s) URL")
		}
	}
	return nil
}

func parseURL(raw string, schemes ...string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return nil, false
	}
	for _, sc := range schemes {
		if u.Scheme == sc {
			return u, true
		}
	}
	return nil, false
}

func validHostPort(v string) bool {
	host, port := v, ""
	if h, p, err := net.SplitHostPort(v); err == nil {
		host, port = h, p
		if p == "" {
			return false
		}
	}
	if host == "" {
		return false
	}
	// Reject anything that would parse as more than a bare host.
	u, err := url.Parse("//" + net.JoinHostPort(host, "0"))
	if err != nil || u.Hostname() != host || u.Path != "" || u.User != nil {
		return false
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}
