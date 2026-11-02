package blue

import (
	"reflect"
	"testing"
)

func TestUUID16String(t *testing.T) {
	t.Parallel()
	if got := UUID16(0xfff0).String(); got != "fff0" {
		t.Fatalf("got %q", got)
	}
	full := "6e400001-b5a3-f393-e0a9-e50e24dcca9e"
	u, err := ParseUUID(full)
	if err != nil || u.String() != full {
		t.Fatalf("got %v %v", u, err)
	}
	s, err := ParseUUID("FFF0")
	if err != nil || s != UUID16(0xfff0) {
		t.Fatalf("got %v %v", s, err)
	}
	if _, err := ParseUUID("zz"); err == nil {
		t.Fatal("want error")
	}
}

func TestParseAddressRandom(t *testing.T) {
	t.Parallel()
	a, err := ParseAddress("aa:bb:cc:dd:ee:ff/random")
	if err != nil {
		t.Fatal(err)
	}
	if a.Type != AddrRandom || a.MAC != [6]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff} {
		t.Fatalf("got %+v", a)
	}
	if a.String() != "AA:BB:CC:DD:EE:FF/random" {
		t.Fatalf("got %q", a.String())
	}
	p, err := ParseAddress("AA:BB:CC:DD:EE:FF")
	if err != nil || p.Type != AddrPublic || p.String() != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("got %+v %v", p, err)
	}
	for _, bad := range []string{"", "AA:BB", "AA:BB:CC:DD:EE:GG", "AA:BB:CC:DD:EE:FF/x"} {
		if _, err := ParseAddress(bad); err == nil {
			t.Fatalf("%q: want error", bad)
		}
	}
}

func TestAddressUint64(t *testing.T) {
	t.Parallel()
	a, _ := ParseAddress("AA:BB:CC:DD:EE:FF")
	if got := a.Uint64(); got != 0xAABBCCDDEEFF {
		t.Fatalf("got %#x", got)
	}
}

func TestParseAdvertisingData(t *testing.T) {
	t.Parallel()
	raw := []byte{0x0a, 0x09, 0x4d, 0x52, 0x4b, 0x2d, 0x53, 0x32, 0x39, 0x52, 0x32, 0x05, 0x03, 0xf0, 0xff, 0x26, 0x18}
	name, svcs := ParseAdvertisingData(raw)
	if name != "MRK-S29R2" {
		t.Fatalf("name %q", name)
	}
	if want := []UUID{UUID16(0xfff0), UUID16(0x1826)}; !reflect.DeepEqual(svcs, want) {
		t.Fatalf("svcs %v", svcs)
	}
}

func TestParseAdvertisingData128AndMalformed(t *testing.T) {
	t.Parallel()
	u, _ := ParseUUID("6e400001-b5a3-f393-e0a9-e50e24dcca9e")
	raw := []byte{0x11, 0x07}
	for i := 15; i >= 0; i-- { // little-endian on the wire
		raw = append(raw, u[i])
	}
	raw = append(raw, 0x03, 0x08, 'a', 'b') // shortened name
	_, svcs := ParseAdvertisingData(raw)
	if len(svcs) != 1 || svcs[0] != u {
		t.Fatalf("svcs %v", svcs)
	}
	name, _ := ParseAdvertisingData(raw)
	if name != "ab" {
		t.Fatalf("name %q", name)
	}
	// truncated / zero-length structures must not panic
	ParseAdvertisingData([]byte{0x00, 0x09, 0x20, 0x09, 'x'})
	ParseAdvertisingData(nil)
}
