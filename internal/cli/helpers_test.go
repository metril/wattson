package cli

import (
	"bytes"
	"context"
	"encoding/binary"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/blue/bluetest"
)

var testAddr = blue.Address{MAC: [6]byte{0xC0, 0xFF, 0xEE, 0x00, 0x00, 0x01}, Type: blue.AddrRandom}

// fitPlusEcho builds a FitPlus fake whose notifications echo the last level written.
func fitPlusEcho() *bluetest.Fake { return newFitPlusFake(func(l int) int { return l }) }

// newFitPlusFake builds a FitPlus fake reporting reportOf(level written); nil = never notifies.
func newFitPlusFake(reportOf func(int) int) *bluetest.Fake {
	var f *bluetest.Fake
	var level atomic.Int32
	dev := &bluetest.Device{
		Adv: blue.Advertisement{Addr: testAddr, Name: "MRK-S29R2", RSSI: -50, Services: []blue.UUID{bike.FitPlusService}},
		Services: []blue.Service{
			{UUID: blue.UUID16(0x1800), Chars: []blue.Characteristic{{UUID: gapDeviceName, Props: blue.PropRead}}},
			{UUID: bike.FitPlusService, Chars: []blue.Characteristic{
				{UUID: bike.FitPlusNotify, Props: blue.PropNotify},
				{UUID: bike.FitPlusWrite, Props: blue.PropWrite},
			}},
		},
		Values: map[blue.UUID][]byte{gapDeviceName: []byte("MRK-S29R2")},
	}
	dev.OnWrite = func(ch blue.UUID, data []byte) {
		if ch != bike.FitPlusWrite || (len(data) > 0 && data[0] == 0xaa) { // init
			if len(data) > 0 && data[0] == 0xaa {
				f.Notify(testAddr, bike.FitPlusNotify, make([]byte, 15)) // handshake answer
			}
			return
		}
		if len(data) == 7 && data[1] == 0x44 && data[2] == 0x05 {
			level.Store(int32(data[3]))
		}
		if l := level.Load(); l > 0 {
			n := make([]byte, 15)
			n[5], n[6] = byte(reportOf(int(l))), 60
			binary.LittleEndian.PutUint16(n[9:], 800)
			f.Notify(testAddr, bike.FitPlusNotify, n)
		}
	}
	if reportOf == nil {
		dev.OnWrite = nil
	}
	f = bluetest.NewFake(dev)
	return f
}

func testDeps(f blue.Transport) deps {
	return deps{
		newTransport: func(context.Context, string, string) (blue.Transport, error) { return f, nil },
		tweakBike: func(o *bike.Options) {
			o.Keepalive = 2 * time.Millisecond
			o.WriteTimeout = time.Second
		},
		now:             func() time.Time { return time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC) },
		selftestTimeout: 30 * time.Millisecond,
	}
}

func runCLI(t *testing.T, d deps, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), d, args, &out, &errb)
	return code, out.String(), errb.String()
}

// levelWrites returns the levels of every set-level frame written, in order.
func levelWrites(f *bluetest.Fake) []int {
	var out []int
	for _, w := range f.Writes(testAddr) {
		if len(w) == 7 && w[0] == 0x02 && w[1] == 0x44 && w[2] == 0x05 {
			out = append(out, int(w[3]))
		}
	}
	return out
}
