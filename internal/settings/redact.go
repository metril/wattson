package settings

import (
	"fmt"
	"log/slog"
)

// Secret-bearing sections implement fmt.Formatter and slog.LogValuer so that
// %v, %+v, %#v and structured logging never print a secret. Set secrets
// show as "[redacted]", unset ones as empty.

const redacted = "[redacted]"

func mask(s *string) {
	if *s != "" {
		*s = redacted
	}
}

func (b Bike) redact() Bike { mask(&b.ESPHomeKey); return b }
func (h HA) redact() HA     { mask(&h.Token); return h }
func (m MQTT) redact() MQTT { mask(&m.Password); return m }
func (m Mapillary) redact() Mapillary {
	mask(&m.Token)
	return m
}
func (s Strava) redact() Strava { mask(&s.ClientSecret); return s }

func (b Bike) Format(f fmt.State, verb rune) {
	type plain Bike
	fmt.Fprintf(f, fmt.FormatString(f, verb), plain(b.redact()))
}
func (h HA) Format(f fmt.State, verb rune) {
	type plain HA
	fmt.Fprintf(f, fmt.FormatString(f, verb), plain(h.redact()))
}
func (m MQTT) Format(f fmt.State, verb rune) {
	type plain MQTT
	fmt.Fprintf(f, fmt.FormatString(f, verb), plain(m.redact()))
}
func (m Mapillary) Format(f fmt.State, verb rune) {
	type plain Mapillary
	fmt.Fprintf(f, fmt.FormatString(f, verb), plain(m.redact()))
}
func (s Strava) Format(f fmt.State, verb rune) {
	type plain Strava
	fmt.Fprintf(f, fmt.FormatString(f, verb), plain(s.redact()))
}

func (b Bike) LogValue() slog.Value {
	type plain Bike
	return slog.AnyValue(plain(b.redact()))
}
func (h HA) LogValue() slog.Value {
	type plain HA
	return slog.AnyValue(plain(h.redact()))
}
func (m MQTT) LogValue() slog.Value {
	type plain MQTT
	return slog.AnyValue(plain(m.redact()))
}
func (m Mapillary) LogValue() slog.Value {
	type plain Mapillary
	return slog.AnyValue(plain(m.redact()))
}
func (s Strava) LogValue() slog.Value {
	type plain Strava
	return slog.AnyValue(plain(s.redact()))
}

// LogValue logs every section, with secrets redacted.
func (s Settings) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("general", s.General), slog.Any("auth", s.Auth),
		slog.Any("bike", s.Bike), slog.Any("ha", s.HA), slog.Any("mqtt", s.MQTT),
		slog.Any("brouter", s.BRouter), slog.Any("mapillary", s.Mapillary),
		slog.Any("strava", s.Strava),
	)
}
