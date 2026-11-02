package ridectl

import (
	"context"
	"time"

	"github.com/metril/wattson/internal/sensors"
)

// hrPoll is how often the connection checks its heart rate source for a
// change worth publishing in the status.
const hrPoll = 250 * time.Millisecond

func infoOf(s sensors.HRStatus) HRInfo {
	return HRInfo{Source: s.Source, Target: s.Target, Connected: s.Connected, Error: s.Error}
}

// hrStatus is the heart rate part of the connection's status, nil when
// the source is off.
func (c *conn) hrStatus() *HRInfo {
	if c.hrMon == nil {
		return nil
	}
	i := infoOf(c.hrMon.Status())
	return &i
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
		last := infoOf(mon.Status())
		t := time.NewTicker(hrPoll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if k := infoOf(mon.Status()); k != last {
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
