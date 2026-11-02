package settings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/metril/wattson/internal/secretbox"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

func newSvc(t *testing.T) (*settings.Service, *store.Store) {
	t.Helper()
	st := storetest.New(t)
	box, err := secretbox.LoadOrCreateKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return settings.NewService(st, box), st
}

func TestGetDefaults(t *testing.T) {
	t.Parallel()
	svc, _ := newSvc(t)
	got, err := svc.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != settings.Defaults() {
		t.Fatalf("got %+v, want defaults", got)
	}
	d := settings.Defaults()
	if !d.Auth.AllowLocalLogin || d.Auth.SessionTTLHours != 168 || d.Bike.Driver != "sim" ||
		d.Bike.Transport != "local" || d.Bike.IdleReleaseMin != 10 || d.Bike.SimCadenceRpm != 80 ||
		d.BRouter.URL != "http://brouter:17777" {
		t.Fatalf("bad defaults: %+v", d)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
}

func TestUpdatePersists(t *testing.T) {
	t.Parallel()
	svc, st := newSvc(t)
	ctx := context.Background()
	if _, err := svc.Update(ctx, func(s *settings.Settings) error {
		s.General.PublicURL = "https://w.example"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx)
	if err != nil || got.General.PublicURL != "https://w.example" {
		t.Fatalf("Get = %+v, %v", got.General, err)
	}
	// Only the changed section is written.
	rows, err := st.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows["general"] == "" {
		t.Fatalf("rows = %v, want only general", rows)
	}
}

func TestUpdatePersistsAcrossService(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	box, err := secretbox.LoadOrCreateKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := settings.NewService(st, box).Update(ctx, func(s *settings.Settings) error {
		s.General.PublicURL = "https://w.example"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := settings.NewService(st, box).Get(ctx)
	if err != nil || got.General.PublicURL != "https://w.example" {
		t.Fatalf("Get = %+v, %v", got.General, err)
	}
}

func TestSecretsSealedAtRest(t *testing.T) {
	t.Parallel()
	svc, st := newSvc(t)
	ctx := context.Background()
	if _, err := svc.Update(ctx, func(s *settings.Settings) error {
		s.HA.Token = "tok-plain-123"
		s.HA.URL = "http://ha.local:8123"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := st.Read.QueryRow(`SELECT value FROM settings WHERE section='ha'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "tok-plain-123") || !strings.Contains(raw, `"v1.`) {
		t.Fatalf("raw row not sealed: %s", raw)
	}
	got, err := svc.Get(ctx)
	if err != nil || got.HA.Token != "tok-plain-123" || got.HA.URL != "http://ha.local:8123" {
		t.Fatalf("Get = %+v, %v", got.HA, err)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	key32 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	tests := []struct {
		name  string
		mod   func(*settings.Settings)
		field string // "" = valid
	}{
		{"defaults", func(*settings.Settings) {}, ""},
		{"ttl zero", func(s *settings.Settings) { s.Auth.SessionTTLHours = 0 }, "auth.session_ttl_hours"},
		{"ttl too big", func(s *settings.Settings) { s.Auth.SessionTTLHours = 8761 }, "auth.session_ttl_hours"},
		{"driver ha", func(s *settings.Settings) { s.Bike.Driver = "ha" }, "bike.driver"},
		{"driver auto", func(s *settings.Settings) { s.Bike.Driver = "auto" }, ""},
		{"transport", func(s *settings.Settings) { s.Bike.Transport = "x" }, "bike.transport"},
		{"public url path", func(s *settings.Settings) { s.General.PublicURL = "https://x/sub" }, "general.public_url"},
		{"public url scheme", func(s *settings.Settings) { s.General.PublicURL = "ftp://x" }, "general.public_url"},
		{"public url relative", func(s *settings.Settings) { s.General.PublicURL = "w.example" }, "general.public_url"},
		{"public url slash", func(s *settings.Settings) { s.General.PublicURL = "https://x/" }, ""},
		{"esphome key short", func(s *settings.Settings) { s.Bike.ESPHomeKey = "short" }, "bike.esphome_key"},
		{"esphome key ok", func(s *settings.Settings) { s.Bike.ESPHomeKey = key32 }, ""},
		{"address bad", func(s *settings.Settings) { s.Bike.Address = "zz" }, "bike.address"},
		{"address ok", func(s *settings.Settings) { s.Bike.Address = "AA:BB:CC:DD:EE:FF/random" }, ""},
		{"esphome host bad", func(s *settings.Settings) { s.Bike.ESPHomeHost = "http://x" }, "bike.esphome_host"},
		{"esphome host port bad", func(s *settings.Settings) { s.Bike.ESPHomeHost = "x:abc" }, "bike.esphome_host"},
		{"esphome host ok", func(s *settings.Settings) { s.Bike.ESPHomeHost = "esp.local:6053" }, ""},
		{"idle", func(s *settings.Settings) { s.Bike.IdleReleaseMin = 241 }, "bike.idle_release_min"},
		{"idle neg", func(s *settings.Settings) { s.Bike.IdleReleaseMin = -1 }, "bike.idle_release_min"},
		{"cadence", func(s *settings.Settings) { s.Bike.SimCadenceRpm = 151 }, "bike.sim_cadence_rpm"},
		{"ha url", func(s *settings.Settings) { s.HA.URL = "ws://x" }, "ha.url"},
		{"brouter url", func(s *settings.Settings) { s.BRouter.URL = "nope" }, "brouter.url"},
		{"mqtt url", func(s *settings.Settings) { s.MQTT.URL = "http://x" }, "mqtt.url"},
		{"ha url userinfo", func(s *settings.Settings) { s.HA.URL = "http://u:p@ha.local:8123" }, "ha.url"},
		{"ha url user only", func(s *settings.Settings) { s.HA.URL = "https://tok@ha.local" }, "ha.url"},
		{"mqtt url userinfo", func(s *settings.Settings) { s.MQTT.URL = "mqtt://u:p@broker:1883" }, "mqtt.url"},
		{"mqtt url ok", func(s *settings.Settings) { s.MQTT.URL = "mqtts://x:8883" }, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := settings.Defaults()
			tc.mod(&s)
			err := s.Validate()
			if tc.field == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var ve *settings.ValidationError
			if !errors.As(err, &ve) || ve.Field != tc.field {
				t.Fatalf("err = %v, want field %s", err, tc.field)
			}
			if ve.Error() != ve.Field+": "+ve.Msg {
				t.Fatalf("Error() = %q", ve.Error())
			}
		})
	}
}

func TestUpdateRejectsInvalidLeavesOld(t *testing.T) {
	t.Parallel()
	svc, _ := newSvc(t)
	ctx := context.Background()
	if _, err := svc.Update(ctx, func(s *settings.Settings) error {
		s.General.PublicURL = "https://ok.example"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Update(ctx, func(s *settings.Settings) error {
		s.General.PublicURL = "https://changed.example"
		s.Auth.SessionTTLHours = 0
		return nil
	})
	var ve *settings.ValidationError
	if !errors.As(err, &ve) || ve.Field != "auth.session_ttl_hours" {
		t.Fatalf("err = %v", err)
	}
	got, err := svc.Get(ctx)
	if err != nil || got.General.PublicURL != "https://ok.example" || got.Auth.SessionTTLHours != 168 {
		t.Fatalf("settings changed: %+v %v", got, err)
	}
	// fn error aborts too.
	boom := errors.New("boom")
	if _, err := svc.Update(ctx, func(s *settings.Settings) error { s.General.PublicURL = "https://z"; return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	got, _ = svc.Get(ctx)
	if got.General.PublicURL != "https://ok.example" {
		t.Fatalf("fn error persisted change: %+v", got.General)
	}
}

func TestDecryptFailureSurfaces(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	boxA, err := secretbox.LoadOrCreateKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	boxB, err := secretbox.LoadOrCreateKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := settings.NewService(st, boxA).Update(ctx, func(s *settings.Settings) error {
		s.Mapillary.Token = "mly-token"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.NewService(st, boxB).Get(ctx); !errors.Is(err, secretbox.ErrDecrypt) {
		t.Fatalf("err = %v, want ErrDecrypt", err)
	}
}

func TestSecretsRedacted(t *testing.T) {
	t.Parallel()
	s := settings.Defaults()
	s.Bike.ESPHomeKey = "SECRET-esphome"
	s.HA.Token = "SECRET-ha"
	s.MQTT.Password = "SECRET-mqtt"
	s.Mapillary.Token = "SECRET-mly"
	s.Strava.ClientSecret = "SECRET-strava"
	var buf bytes.Buffer
	for _, h := range []slog.Handler{slog.NewJSONHandler(&buf, nil), slog.NewTextHandler(&buf, nil)} {
		slog.New(h).Info("x", "settings", s, "ha", s.HA, "bike", &s.Bike)
	}
	outs := []string{
		buf.String(),
		fmt.Sprintf("%v %+v %#v %s", s, s, s, s.HA),
		fmt.Sprintf("%v %+v %#v", &s, s.Strava, s.Bike),
	}
	for _, out := range outs {
		if strings.Contains(out, "SECRET") {
			t.Fatalf("secret leaked: %s", out)
		}
	}
	if !strings.Contains(outs[1], "[redacted]") || !strings.Contains(outs[1], "ClientID") {
		t.Fatalf("unexpected format: %s", outs[1])
	}
}

func TestSealedValueBoundToField(t *testing.T) {
	t.Parallel()
	svc, st := newSvc(t)
	ctx := context.Background()
	if _, err := svc.Update(ctx, func(s *settings.Settings) error {
		s.HA.Token = "tok"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Move the sealed ha.token ciphertext into mapillary.token.
	rows, _ := st.GetSettings(ctx)
	var ha struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(rows["ha"]), &ha); err != nil {
		t.Fatal(err)
	}
	moved, _ := json.Marshal(map[string]string{"token": ha.Token})
	if err := st.PutSettings(ctx, map[string]string{"mapillary": string(moved)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx); !errors.Is(err, secretbox.ErrDecrypt) {
		t.Fatalf("err = %v, want ErrDecrypt", err)
	}
}
