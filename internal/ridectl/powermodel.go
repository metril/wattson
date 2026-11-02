package ridectl

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
)

// PowerModelStore persists learned power models per bike key (*store.Store).
type PowerModelStore interface {
	PowerModel(ctx context.Context, key string) ([]byte, error)
	PutPowerModel(ctx context.Context, key string, b []byte) error
	DeletePowerModel(ctx context.Context, key string) error
}

// BikeKey identifies the bike a power model belongs to: "sim", "ble:<ADDRESS
// upper case>" for Bluetooth bikes or "ha:<resistance entity>" for Home
// Assistant bikes.
func BikeKey(s settings.Settings) string {
	switch b := s.Bike; b.Driver {
	case "sim":
		return "sim"
	case "ha":
		return "ha:" + strings.TrimSpace(b.HAResistanceEntity)
	default:
		return "ble:" + strings.ToUpper(strings.TrimSpace(b.Address))
	}
}

// defaultMaxLevel is the number of levels reported for an idle bike (no rider
// is bound, so no rider maximum is known); most bikes have 16.
const defaultMaxLevel = 16

// PowerModelInfo is the learned power model of one bike as the UI shows it.
type PowerModelInfo struct {
	BikeKey   string    `json:"bike_key" doc:"Bike the model belongs to: sim, ble:<ADDRESS> or ha:<resistance entity>."`
	A         float64   `json:"a" doc:"Fitted intercept: watts per rpm at level 0."`
	B         float64   `json:"b" doc:"Fitted slope: watts per rpm per resistance level."`
	Samples   int       `json:"samples" doc:"Number of settled ride samples the fit has learned from."`
	Seeded    bool      `json:"seeded" doc:"True while the model has learned nothing yet and still equals the built-in starting model."`
	WattsAt80 []float64 `json:"watts_at_80" doc:"Modelled bike power in watts at 80 rpm for levels 1 up to the bound rider's maximum level (16 when no rider is bound). Index 0 is level 1."`
}

func modelInfo(key string, st bike.PowerModelState, max int) PowerModelInfo {
	m := bike.NewPowerModel(st)
	p := m.Params()
	w := make([]float64, max)
	for i := range w {
		w[i] = m.Power(i+1, 80)
	}
	return PowerModelInfo{BikeKey: key, A: p.A, B: p.B, Samples: m.State().N, Seeded: m.State().N == 0, WattsAt80: w}
}

// loadModel reads the stored model for key; a missing, unreadable or
// unparsable row yields a seeded model. The returned count is the sample
// count that is already stored.
func (m *Manager) loadModel(ctx context.Context, key string) (*bike.PowerModel, int) {
	if m.d.PowerModels == nil {
		return bike.NewPowerModel(bike.SeedState()), 0
	}
	b, err := m.d.PowerModels.PowerModel(ctx, key)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			m.log.Warn("power model load failed", "key", key, "err", err)
		}
		return bike.NewPowerModel(bike.SeedState()), 0
	}
	var st bike.PowerModelState
	if err := json.Unmarshal(b, &st); err != nil {
		m.log.Warn("power model unparsable, using the seed", "key", key, "err", err)
		return bike.NewPowerModel(bike.SeedState()), 0
	}
	pm := bike.NewPowerModel(st)
	return pm, pm.State().N
}

// saveModel persists c's model when it learned something since the last save.
// It runs when an engine's Run returns, so a stop, a release and a close all
// store what the ride learned.
func (m *Manager) saveModel(c *conn) {
	if m.d.PowerModels == nil || c.model == nil {
		return
	}
	st := c.model.State()
	m.mu.Lock()
	same := st.N == c.savedN
	m.mu.Unlock()
	if same {
		return
	}
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	// c.ctx may already be cancelled by a release: save on a fresh context.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.d.PowerModels.PutPowerModel(ctx, c.key, b); err != nil {
		m.log.Warn("power model save failed", "key", c.key, "err", err)
		return
	}
	m.mu.Lock()
	c.savedN = st.N
	m.mu.Unlock()
}

// PowerModelInfo returns the learned power model of the connected bike or,
// when idle, of the bike the settings name.
func (m *Manager) PowerModelInfo(ctx context.Context) (PowerModelInfo, error) {
	m.mu.Lock()
	if c := m.cur; c != nil && c.model != nil {
		info := modelInfo(c.key, c.model.State(), c.rider.MaxLevel)
		m.mu.Unlock()
		return info, nil
	}
	m.mu.Unlock()
	s, err := m.d.Settings(ctx)
	if err != nil {
		return PowerModelInfo{}, err
	}
	key := BikeKey(s)
	pm, _ := m.loadModel(ctx, key)
	return modelInfo(key, pm.State(), defaultMaxLevel), nil
}

// ResetPowerModel deletes the stored model of the connected (or, when idle,
// the configured) bike. A connected bike's live model returns to the seed in
// place, so the running engine uses it at once.
func (m *Manager) ResetPowerModel(ctx context.Context) error {
	m.mu.Lock()
	c := m.cur
	key := ""
	if c != nil && c.model != nil {
		key = c.key
		c.model.Reset()
		c.savedN = 0
	}
	m.mu.Unlock()
	if c == nil || key == "" {
		s, err := m.d.Settings(ctx)
		if err != nil {
			return err
		}
		key = BikeKey(s)
	}
	if m.d.PowerModels == nil {
		return nil
	}
	return m.d.PowerModels.DeletePowerModel(ctx, key)
}
