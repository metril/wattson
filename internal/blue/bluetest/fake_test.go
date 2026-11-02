package bluetest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/metril/wattson/internal/blue"
)

var (
	testAddr = blue.Address{MAC: [6]byte{1, 2, 3, 4, 5, 6}}
	svcUUID  = blue.UUID16(0xfff0)
	notifyCh = blue.UUID16(0xfff1)
	writeCh  = blue.UUID16(0xfff2)
	readCh   = blue.UUID16(0x2a00)
)

func newTestFake() (*Fake, *Device) { return newTestFakeAt(testAddr) }

func newTestFakeAt(addr blue.Address) (*Fake, *Device) {
	dev := &Device{
		Adv: blue.Advertisement{Addr: addr, Name: "MRK-S29R2", RSSI: -60, Services: []blue.UUID{svcUUID}},
		Services: []blue.Service{{UUID: svcUUID, Chars: []blue.Characteristic{
			{UUID: notifyCh, Handle: 3, Props: blue.PropNotify},
			{UUID: writeCh, Handle: 5, Props: blue.PropWrite | blue.PropWriteNoResp},
			{UUID: readCh, Handle: 7, Props: blue.PropRead},
		}}},
		Values: map[blue.UUID][]byte{readCh: []byte("MRK")},
	}
	return NewFake(dev), dev
}

func TestFakeContract(t *testing.T) {
	t.Parallel()
	random := testAddr
	random.Type = blue.AddrRandom
	for name, addr := range map[string]blue.Address{"public": testAddr, "random": random} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			RunContract(t, func(t *testing.T) Harness {
				f, _ := newTestFakeAt(addr)
				t.Cleanup(func() { f.Close() })
				return Harness{
					Transport: f, Dev: addr,
					NotifyChar: notifyCh, WriteChar: writeCh,
					ReadChar: readCh, ReadValue: []byte("MRK"),
					Push:   func(b []byte) { f.Notify(addr, notifyCh, b) },
					Drop:   func() { f.DropLink(addr) },
					Writes: func() [][]byte { return f.Writes(addr) },
				}
			})
		})
	}
}

func TestFakeOnWriteAndWrites(t *testing.T) {
	t.Parallel()
	f, dev := newTestFake()
	defer f.Close()
	var got []byte
	dev.OnWrite = func(ch blue.UUID, d []byte) {
		if ch == writeCh {
			got = append([]byte(nil), d...)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := f.Connect(ctx, testAddr)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, writeCh, []byte{1, 2}, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []byte{1, 2}) || !reflect.DeepEqual(f.Writes(testAddr), [][]byte{{1, 2}}) {
		t.Fatalf("got %v %v", got, f.Writes(testAddr))
	}
}

func TestFakeReadMissing(t *testing.T) {
	t.Parallel()
	f, _ := newTestFake()
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := f.Connect(ctx, testAddr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(ctx, notifyCh); !errors.Is(err, blue.ErrNoCharacteristic) {
		t.Fatalf("got %v", err)
	}
}

func TestFakeCloseUnblocksScanAndConns(t *testing.T) {
	t.Parallel()
	f, _ := newTestFake()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := f.Connect(ctx, testAddr)
	if err != nil {
		t.Fatal(err)
	}
	scanDone := make(chan error, 1)
	go func() { scanDone <- f.Scan(ctx, func(blue.Advertisement) {}) }()
	f.Close()
	select {
	case err := <-scanDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("scan did not exit on Close")
	}
	select {
	case <-c.Done():
	case <-ctx.Done():
		t.Fatal("conn not closed by Fake.Close")
	}
}
