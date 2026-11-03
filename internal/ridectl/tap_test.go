package ridectl

import (
	"testing"

	"github.com/metril/wattson/internal/bike"
)

type armCounter struct {
	bike.Driver
	ev    chan bike.Event
	armed int
}

func (a *armCounter) Events() <-chan bike.Event { return a.ev }
func (a *armCounter) ArmWatchdog()              { a.armed++ }

// The engine reaches the supervisor's watchdog only through the tap.
func TestTapForwardsArmWatchdog(t *testing.T) {
	d := &armCounter{ev: make(chan bike.Event)}
	tp := newTap(d, func(bike.Event) {})
	defer close(d.ev)
	var drv bike.Driver = tp
	a, ok := drv.(interface{ ArmWatchdog() })
	if !ok {
		t.Fatal("tap does not expose ArmWatchdog")
	}
	a.ArmWatchdog()
	if d.armed != 1 {
		t.Fatalf("armed = %d, want 1", d.armed)
	}
}
