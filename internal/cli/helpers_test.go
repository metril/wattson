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

// newFTMSFake builds an FTMS bike. Set Target Resistance writes are echoed as
// Indoor Bike Data with that resistance level. With answer, every Control Point
// write is acknowledged (80 <op> 01).
func newFTMSFake(answer bool) *bluetest.Fake {
	var f *bluetest.Fake
	dev := &bluetest.Device{
		Adv: blue.Advertisement{Addr: testAddr, Name: "FTMS-BIKE", RSSI: -50, Services: []blue.UUID{bike.FTMSService}},
		Services: []blue.Service{{UUID: bike.FTMSService, Chars: []blue.Characteristic{
			{UUID: bike.FTMSIndoorBikeData, Props: blue.PropNotify},
			{UUID: bike.FTMSControlPoint, Props: blue.PropWrite | blue.PropIndicate},
			{UUID: bike.FTMSResistanceRange, Props: blue.PropRead},
		}}},
		// min 1.0, max 10.0, step 1.0 (0.1 units)
		Values: map[blue.UUID][]byte{bike.FTMSResistanceRange: {10, 0, 100, 0, 10, 0}},
	}
	dev.OnWrite = func(ch blue.UUID, data []byte) {
		if ch != bike.FTMSControlPoint || len(data) == 0 {
			return
		}
		if data[0] == 0x04 && len(data) == 2 { // u8 tenths
			ibd := []byte{0x65, 0x00, 120, 0, byte(data[1] / 10), 0, 80, 0} // cadence 60 rpm, level, 80 W
			f.Notify(testAddr, bike.FTMSIndoorBikeData, ibd)
		}
		if answer {
			f.Notify(testAddr, bike.FTMSControlPoint, []byte{0x80, data[0], 0x01})
		}
	}
	f = bluetest.NewFake(dev)
	return f
}

// flakyTransport wraps a Transport so tests can make level writes fail.
type flakyTransport struct {
	blue.Transport
	failFrom   int         // fail level writes numbered >= failFrom (1-based); 0 = never
	failOnly   bool        // fail only the failFrom-th level write
	armed      atomic.Bool // when set, fail every level write
	err        error
	levelWrote atomic.Int32 // level writes seen
}

func (t *flakyTransport) Connect(ctx context.Context, a blue.Address) (blue.Conn, error) {
	c, err := t.Transport.Connect(ctx, a)
	if err != nil {
		return nil, err
	}
	return &flakyConn{Conn: c, t: t}, nil
}

type flakyConn struct {
	blue.Conn
	t *flakyTransport
}

func (c *flakyConn) Write(ctx context.Context, ch blue.UUID, data []byte, resp bool) error {
	if len(data) == 7 && data[0] == 0x02 && data[1] == 0x44 && data[2] == 0x05 {
		n := int(c.t.levelWrote.Add(1))
		switch {
		case c.t.armed.Load(),
			c.t.failFrom > 0 && !c.t.failOnly && n >= c.t.failFrom,
			c.t.failFrom > 0 && c.t.failOnly && n == c.t.failFrom:
			return c.t.err
		}
	}
	return c.Conn.Write(ctx, ch, data, resp)
}
