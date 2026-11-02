package ridectl

import (
	"context"
	"sort"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/sensors"
)

const (
	defaultScan = 5 * time.Second
	maxScan     = 30 * time.Second
)

// ScanRequest selects what to scan with; empty fields fall back to settings.
type ScanRequest struct {
	Transport string `json:"transport,omitempty" doc:"local or esphome; empty uses the saved setting."`
	Host      string `json:"host,omitempty" doc:"ESPHome proxy host or host:port; empty uses the saved setting."`
	Key       string `json:"key,omitempty" doc:"ESPHome API noise key (base64); empty uses the saved setting."`
	DurationS int    `json:"duration_s,omitempty" doc:"How long to scan, in seconds; default 5, maximum 30."`
}

// Device is one advertising BLE device.
type Device struct {
	Address  string `json:"address" doc:"Bluetooth address, with a /random suffix for random addresses."`
	Name     string `json:"name" doc:"Advertised local name, empty when none was seen."`
	RSSI     int    `json:"rssi" doc:"Strongest signal strength seen, in dBm."`
	Protocol string `json:"protocol" doc:"fitplus or ftms when the advertised services match a supported bike, hr for a heart rate monitor; empty otherwise."`
}

// Scan lists nearby devices. It is refused (ErrBusy) unless the manager is
// idle, because one adapter or proxy cannot scan while holding a bike.
func (m *Manager) Scan(ctx context.Context, q ScanRequest) ([]Device, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	if m.cur != nil || m.scanning {
		m.mu.Unlock()
		return nil, ErrBusy
	}
	m.scanning = true
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.scanning = false
		m.mu.Unlock()
		m.wg.Done()
	}()

	s, err := m.d.Settings(ctx)
	if err != nil {
		return nil, err
	}
	kind, host, key := q.Transport, q.Host, q.Key
	if kind == "" {
		kind = s.Bike.Transport
	}
	if host == "" {
		host = s.Bike.ESPHomeHost
	}
	if key == "" {
		key = s.Bike.ESPHomeKey
	}
	dur := time.Duration(min(q.DurationS, int(maxScan/time.Second))) * time.Second
	if dur <= 0 {
		dur = defaultScan
	}

	// Closing the manager ends an in-flight scan.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(m.root, cancel)
	defer stop()

	tr, err := m.openTransport(ctx, kind, host, key)
	if err != nil {
		return nil, err
	}
	defer tr.Close()

	sctx, scancel := context.WithTimeout(ctx, dur)
	defer scancel()
	seen := map[blue.Address]blue.Advertisement{}
	if err := tr.Scan(sctx, func(a blue.Advertisement) {
		if prev, ok := seen[a.Addr]; ok {
			if a.Name == "" {
				a.Name = prev.Name
			}
			if len(a.Services) == 0 {
				a.Services = prev.Services
			}
			a.RSSI = max(a.RSSI, prev.RSSI)
		}
		seen[a.Addr] = a
	}); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := make([]Device, 0, len(seen))
	for _, a := range seen {
		out = append(out, Device{Address: a.Addr.String(), Name: a.Name, RSSI: a.RSSI, Protocol: protocolOf(a.Services)})
	}
	sort.Slice(out, func(i, j int) bool {
		ki, kj := protocolRank(out[i].Protocol), protocolRank(out[j].Protocol)
		if ki != kj {
			return ki < kj
		}
		if out[i].RSSI != out[j].RSSI {
			return out[i].RSSI > out[j].RSSI
		}
		return out[i].Address < out[j].Address
	})
	return out, nil
}

// protocolOf names what a device advertises: a bike protocol wins over the
// heart rate service.
func protocolOf(svcs []blue.UUID) string {
	hr := false
	for _, u := range svcs {
		switch u {
		case bike.FitPlusService:
			return "fitplus"
		case bike.FTMSService:
			return "ftms"
		case sensors.HRService:
			hr = true
		}
	}
	if hr {
		return "hr"
	}
	return ""
}

// protocolRank orders bikes first, then heart rate monitors, then the rest.
func protocolRank(p string) int {
	switch p {
	case "":
		return 2
	case "hr":
		return 1
	}
	return 0
}
