package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/metril/wattson/internal/secretbox"
	"github.com/metril/wattson/internal/store"
)

// Service reads and writes Settings in the store, sealing secret fields.
// Run exactly one Service per process: Update's read-modify-write is only
// serialised by this Service's own mutex.
type Service struct {
	st  *store.Store
	box *secretbox.Box
	mu  sync.Mutex // serialises Update's read-modify-write
}

func NewService(st *store.Store, box *secretbox.Box) *Service {
	return &Service{st: st, box: box}
}

// section binds a stored section name to its struct and secret fields.
type section struct {
	name    string
	ptr     any
	secrets []secret
}

// secret is a sealed field; aad binds the ciphertext to "section.field".
type secret struct {
	aad string
	p   *string
}

func sections(s *Settings) []section {
	return []section{
		{"general", &s.General, nil},
		{"auth", &s.Auth, nil},
		{"bike", &s.Bike, []secret{{"bike.esphome_key", &s.Bike.ESPHomeKey}}},
		{"ha", &s.HA, []secret{{"ha.token", &s.HA.Token}}},
		{"mqtt", &s.MQTT, []secret{{"mqtt.password", &s.MQTT.Password}}},
		{"brouter", &s.BRouter, nil},
		{"mapillary", &s.Mapillary, []secret{{"mapillary.token", &s.Mapillary.Token}}},
		{"strava", &s.Strava, []secret{{"strava.client_secret", &s.Strava.ClientSecret}}},
	}
}

// Get returns Defaults overlaid with the stored sections, reading the
// database on every call.
func (s *Service) Get(ctx context.Context) (Settings, error) {
	rows, err := s.st.GetSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	out := Defaults()
	for _, sec := range sections(&out) {
		raw, ok := rows[sec.name]
		if !ok {
			continue
		}
		if err := json.Unmarshal([]byte(raw), sec.ptr); err != nil {
			return Settings{}, fmt.Errorf("settings: decode %s: %w", sec.name, err)
		}
		for _, f := range sec.secrets {
			plain, err := s.box.OpenFor(*f.p, f.aad)
			if err != nil {
				return Settings{}, fmt.Errorf("settings: open %s: %w", f.aad, err)
			}
			*f.p = plain
		}
	}
	return out, nil
}

// Update applies fn to the current settings, validates the result and
// writes the sections that changed, with secrets sealed, in one transaction.
func (s *Service) Update(ctx context.Context, fn func(*Settings) error) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, err := s.Get(ctx)
	if err != nil {
		return Settings{}, err
	}
	next := cur
	if err := fn(&next); err != nil {
		return Settings{}, err
	}
	if err := next.Validate(); err != nil {
		return Settings{}, err
	}

	// Compare plaintext JSON (sealing is randomised), then seal only what changed.
	sealed := next
	oldSecs, newSecs, sealSecs := sections(&cur), sections(&next), sections(&sealed)
	changed := map[string]string{}
	for i, sec := range newSecs {
		nb, err := json.Marshal(sec.ptr)
		if err != nil {
			return Settings{}, fmt.Errorf("settings: encode %s: %w", sec.name, err)
		}
		ob, err := json.Marshal(oldSecs[i].ptr)
		if err != nil {
			return Settings{}, fmt.Errorf("settings: encode %s: %w", sec.name, err)
		}
		if string(nb) == string(ob) {
			continue
		}
		for _, f := range sealSecs[i].secrets {
			*f.p = s.box.SealFor(*f.p, f.aad)
		}
		sb, err := json.Marshal(sealSecs[i].ptr)
		if err != nil {
			return Settings{}, fmt.Errorf("settings: encode %s: %w", sec.name, err)
		}
		changed[sec.name] = string(sb)
	}
	if len(changed) > 0 {
		if err := s.st.PutSettings(ctx, changed); err != nil {
			return Settings{}, err
		}
	}
	return next, nil
}
