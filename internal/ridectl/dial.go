package ridectl

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
)

const (
	// silenceTimeout matches `wattson ride`/`drive`: how long a real bike may
	// go without telemetry before the link is treated as down (ESPHome
	// proxies can leave a dead link looking alive).
	silenceTimeout = 5 * time.Second
	esphomePort    = "6053"
)

// plan is the part of the settings one bike connection needs.
type plan struct {
	driver    string // auto | fitplus | ftms | sim
	transport string // local | esphome
	addr      blue.Address
	host, key string
	simRpm    float64
}

func (p plan) isSim() bool { return p.driver == "sim" }

func planFor(s settings.Settings) (plan, error) {
	b := s.Bike
	p := plan{driver: b.Driver, transport: b.Transport, host: b.ESPHomeHost, key: b.ESPHomeKey, simRpm: float64(b.SimCadenceRpm)}
	if p.isSim() {
		return p, nil
	}
	if strings.TrimSpace(b.Address) == "" {
		return plan{}, ErrNoAddress
	}
	a, err := blue.ParseAddress(b.Address)
	if err != nil {
		return plan{}, fmt.Errorf("bike address: %w", err)
	}
	p.addr = a
	if p.transport == "esphome" && strings.TrimSpace(p.host) == "" {
		return plan{}, ErrNoHost
	}
	return p, nil
}

// hostPort adds the default ESPHome API port when host has none.
func hostPort(host string) string {
	host = strings.TrimSpace(host)
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), esphomePort)
}

// openTransport opens the BLE transport named by kind (local or esphome).
func (m *Manager) openTransport(ctx context.Context, kind, host, key string) (blue.Transport, error) {
	switch kind {
	case "local", "":
		return m.d.OpenLocal()
	case "esphome":
		if strings.TrimSpace(host) == "" {
			return nil, ErrNoHost
		}
		return m.d.OpenESPHome(ctx, hostPort(host), key)
	}
	return nil, fmt.Errorf("unknown transport %q", kind)
}

// dialFor builds the session dial for rider r. tr is the connection's shared
// transport (nil for the simulator).
func (m *Manager) dialFor(p plan, r store.Rider, tr blue.Transport) bike.Dial {
	if p.isSim() {
		return m.d.SimDial(sim.Config{
			MaxLevel: r.MaxLevel, MinLevel: r.MinLevel,
			Cadence: sim.Const(p.simRpm), Period: m.d.SimPeriod,
		})
	}
	o := bike.Options{MinLevel: r.MinLevel, MaxLevel: r.MaxLevel, SilenceTimeout: silenceTimeout}
	switch p.driver {
	case "fitplus":
		o.Protocol = bike.ProtoFitPlus
	case "ftms":
		o.Protocol = bike.ProtoFTMS
	}
	return bike.BLEDial(tr, p.addr, o)
}
