package ridectl

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/habike"
	"github.com/metril/wattson/internal/bike/sim"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/hass"
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
	driver    string // auto | fitplus | ftms | ha | sim
	transport string // local | esphome
	addr      blue.Address
	host, key string
	simRpm    float64

	// Home Assistant (driver ha, or heart rate source ha).
	haURL, haToken                   string
	haResistance, haPower, haCadence string

	// Optional heart rate source: "", ble or ha.
	hrSource string
	hrAddr   blue.Address
	hrName   string
	hrEntity string
	hrErr    error // unusable heart rate settings; hrSource is then ""
}

func (p plan) isSim() bool { return p.driver == "sim" }
func (p plan) isHA() bool  { return p.driver == "ha" }

// bleBike reports that the bike itself is reached over Bluetooth.
func (p plan) bleBike() bool { return !p.isSim() && !p.isHA() }

// needsTransport reports that a Bluetooth transport is shared by this
// connection (the bike and/or the heart rate monitor use it).
func (p plan) needsTransport() bool { return p.bleBike() || p.hrSource == "ble" }

func planFor(s settings.Settings) (plan, error) {
	b := s.Bike
	p := plan{driver: b.Driver, transport: b.Transport, host: b.ESPHomeHost, key: b.ESPHomeKey, simRpm: float64(b.SimCadenceRpm),
		haURL: s.HA.URL, haToken: s.HA.Token,
		haResistance: b.HAResistanceEntity, haPower: b.HAPowerEntity, haCadence: b.HACadenceEntity}
	switch hr := s.HeartRate; hr.Source {
	case "ble":
		// Heart rate is optional: unusable settings are reported in hrErr and
		// the bike connects without it.
		a, err := blue.ParseAddress(hr.Address)
		if err != nil {
			p.hrErr = fmt.Errorf("heart rate address: %w", err)
		} else {
			p.hrSource, p.hrAddr, p.hrName = "ble", a, hr.Name
		}
	case "ha":
		p.hrSource, p.hrEntity = "ha", hr.HAEntity
	}
	if p.bleBike() {
		if strings.TrimSpace(b.Address) == "" {
			return plan{}, ErrNoAddress
		}
		a, err := blue.ParseAddress(b.Address)
		if err != nil {
			return plan{}, fmt.Errorf("bike address: %w", err)
		}
		p.addr = a
	}
	if p.needsTransport() && p.transport == "esphome" && strings.TrimSpace(p.host) == "" {
		if p.bleBike() {
			return plan{}, ErrNoHost
		}
		// Only the heart rate monitor wanted the proxy.
		p.hrSource, p.hrErr = "", fmt.Errorf("heart rate: %w", ErrNoHost)
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
		return m.openLocal(ctx)
	case "esphome":
		if strings.TrimSpace(host) == "" {
			return nil, ErrNoHost
		}
		return m.d.OpenESPHome(ctx, hostPort(host), key)
	}
	return nil, fmt.Errorf("unknown transport %q", kind)
}

// sharedFor returns the connection's shared Bluetooth transport, nil when
// neither the bike nor the heart rate monitor uses Bluetooth.
func (m *Manager) sharedFor(p plan) blue.Transport {
	if !p.needsTransport() {
		return nil
	}
	return newShared(func(ctx context.Context) (blue.Transport, error) {
		return m.openTransport(ctx, p.transport, p.host, p.key)
	})
}

// openHA returns the Home Assistant dialer the bike driver and the heart rate
// monitor use; each caller owns the client it gets.
func (m *Manager) openHA(p plan) func(ctx context.Context) (*hass.Client, error) {
	return func(ctx context.Context) (*hass.Client, error) {
		switch {
		case strings.TrimSpace(p.haURL) == "":
			return nil, bike.ConfigError(ErrNoHAURL)
		case p.haToken == "":
			return nil, bike.ConfigError(ErrNoHAToken)
		}
		return m.d.OpenHA(ctx, p.haURL, p.haToken)
	}
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
	if p.isHA() {
		return habike.Dial(habike.Config{
			Dial: m.openHA(p), Resistance: p.haResistance, Power: p.haPower, Cadence: p.haCadence,
			MinLevel: r.MinLevel, MaxLevel: r.MaxLevel, Period: m.d.HAPeriod,
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

// openLocal runs Deps.OpenLocal (which takes no context and may block on the
// BlueZ adapter) so that ctx can abandon it; a transport that arrives after
// the abandon is closed.
func (m *Manager) openLocal(ctx context.Context) (blue.Transport, error) {
	type result struct {
		tr  blue.Transport
		err error
	}
	ch := make(chan result, 1)
	go func() {
		tr, err := m.d.OpenLocal()
		ch <- result{tr, err}
	}()
	select {
	case r := <-ch:
		return r.tr, r.err
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.tr != nil {
				_ = r.tr.Close()
			}
		}()
		return nil, ctx.Err()
	}
}
