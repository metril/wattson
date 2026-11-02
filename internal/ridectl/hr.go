package ridectl

import (
	"context"
	"time"

	"github.com/metril/wattson/internal/sensors"
)

// hrPoll is how often the connection checks its heart rate source for a
// change worth publishing in the status.
const hrPoll = 250 * time.Millisecond

// hrKey is the part of an HRStatus that appears in Status; readings
// (BPM, LastAt) change every second and travel in the ticks instead.
type hrKey struct {
	source, target, errText string
	connected               bool
}

func keyOf(s sensors.HRStatus) hrKey {
	return hrKey{source: s.Source, target: s.Target, errText: s.Error, connected: s.Connected}
}

// hrStatus is the heart rate part of the connection's status, nil when
// the source is off. The live reading is left out (see hrKey).
func (c *conn) hrStatus() *sensors.HRStatus {
	if c.hrMon == nil {
		return nil
	}
	st := c.hrMon.Status()
	st.BPM, st.LastAt = 0, time.Time{}
	return &st
}

// startHR runs the connection's heart rate source from now until stopHR. It
// outlives engine swaps and never touches the ride: failures only show in the
// status. The caller holds m.mu.
func (m *Manager) startHR(c *conn) {
	var mon sensors.Monitor
	switch c.plan.hrSource {
	case "ble":
		mon = sensors.NewBLE(sensors.BLEConfig{Transport: c.tr, Addr: c.plan.hrAddr, Name: c.plan.hrName, BackoffMin: m.d.HRBackoffMin})
	case "ha":
		mon = sensors.NewHA(sensors.HAConfig{Dial: m.openHA(c.plan), Entity: c.plan.hrEntity, BackoffMin: m.d.HRBackoffMin})
	default:
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.hrMon, c.hrCancel, c.hrDone = mon, cancel, make(chan struct{})
	done := c.hrDone
	m.wg.Add(2)
	go func() {
		defer m.wg.Done()
		defer close(done)
		_ = mon.Run(ctx)
	}()
	go func() {
		defer m.wg.Done()
		last := keyOf(mon.Status())
		t := time.NewTicker(hrPoll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if k := keyOf(mon.Status()); k != last {
				last = k
				m.mu.Lock()
				if m.cur == c && !c.finished {
					m.pushLocked(c)
				}
				m.mu.Unlock()
			}
		}
	}()
}

// stopHR ends the heart rate source and waits for it.
func (c *conn) stopHR() {
	if c.hrCancel == nil {
		return
	}
	c.hrCancel()
	<-c.hrDone
}
