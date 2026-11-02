package bike

import (
	"bytes"
	"errors"
	"testing"
)

func TestFitPlusLevel1(t *testing.T) {
	t.Parallel()
	got, err := FitPlusSetLevel(1)
	want := []byte{0x02, 0x44, 0x05, 0x01, 0xf9, 0xb9, 0x03}
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got % x err %v want % x", got, err, want)
	}
}

func TestFitPlusLevel16(t *testing.T) {
	t.Parallel()
	got, err := FitPlusSetLevel(16)
	want := []byte{0x02, 0x44, 0x05, 0x10, 0x06, 0x57, 0x03}
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got % x err %v want % x", got, err, want)
	}
}

func TestFitPlusLevel0Rejected(t *testing.T) {
	t.Parallel()
	for _, l := range []int{0, -1, 33, 1 << 40} {
		b, err := FitPlusSetLevel(l)
		if !errors.Is(err, ErrLevelRange) || b != nil {
			t.Errorf("level %d: got % x err %v", l, b, err)
		}
	}
}

func TestFitPlusChecksumXOR(t *testing.T) {
	t.Parallel()
	for lvl := 1; lvl <= 32; lvl++ {
		row := fitPlusLevels[lvl-1]
		if want := 0x44 ^ 0x05 ^ byte(lvl) ^ row[0]; row[1] != want {
			t.Errorf("level %d: CC=%02x want %02x", lvl, row[1], want)
		}
		f, err := FitPlusSetLevel(lvl)
		if err != nil || f[3] != byte(lvl) || f[4] != row[0] || f[5] != row[1] {
			t.Errorf("level %d: frame % x err %v", lvl, f, err)
		}
	}
}

func TestFitPlusTableExact(t *testing.T) {
	t.Parallel()
	want := [32][2]byte{
		{0xf9, 0xb9}, {0xf9, 0xba}, {0xfa, 0xb8}, {0xfb, 0xbe}, {0xfc, 0xb8}, {0xfd, 0xba}, {0xfe, 0xb8}, {0xff, 0xb6},
		{0x00, 0x48}, {0x00, 0x4b}, {0x01, 0x4b}, {0x03, 0x4e}, {0x03, 0x4f}, {0x05, 0x4a}, {0x05, 0x4b}, {0x06, 0x57},
		{0x07, 0x57}, {0x08, 0x5b}, {0x09, 0x5b}, {0x0a, 0x5f}, {0x0b, 0x5f}, {0x0c, 0x5b}, {0x0d, 0x5b}, {0x0e, 0x57},
		{0x00, 0x58}, {0x00, 0x5b}, {0x00, 0x5a}, {0x00, 0x5d}, {0x00, 0x5c}, {0x00, 0x5f}, {0x00, 0x5e}, {0x00, 0x61},
	}
	if fitPlusLevels != want {
		t.Fatalf("table mismatch:\n got %x\nwant %x", fitPlusLevels, want)
	}
}

func TestFitPlusInitKeepalive(t *testing.T) {
	t.Parallel()
	if got := FitPlusInit(); !bytes.Equal(got, []byte{0xaa, 0x01, 0x00, 0x01, 0x55}) {
		t.Errorf("init % x", got)
	}
	even := []byte{0x02, 0x42, 0x42, 0x03}
	odd := []byte{0x02, 0x43, 0x01, 0x42, 0x03}
	for _, i := range []int{0, 2, 100, -2} {
		if got := FitPlusKeepalive(i); !bytes.Equal(got, even) {
			t.Errorf("keepalive(%d) % x", i, got)
		}
	}
	for _, i := range []int{1, 3, 101, -1, -3} {
		if got := FitPlusKeepalive(i); !bytes.Equal(got, odd) {
			t.Errorf("keepalive(%d) % x", i, got)
		}
	}
}

func TestFitPlusUUIDs(t *testing.T) {
	t.Parallel()
	if FitPlusService.String() != "fff0" || FitPlusNotify.String() != "fff1" || FitPlusWrite.String() != "fff2" {
		t.Fatal(FitPlusService, FitPlusNotify, FitPlusWrite)
	}
}

func TestParseFitPlus(t *testing.T) {
	t.Parallel()
	b := make([]byte, 15)
	b[5], b[6], b[9], b[10] = 7, 82, 0xd0, 0x07
	tel, ok := ParseFitPlus(b)
	if !ok || tel.Level != 7 || tel.Cadence != 82 || tel.PowerW != 200.0 || tel.SpeedKmh != nil || !tel.At.IsZero() {
		t.Fatalf("%+v ok=%v", tel, ok)
	}
	for _, n := range []int{0, 13, 14, 16} {
		if _, ok := ParseFitPlus(make([]byte, n)); ok {
			t.Errorf("len %d accepted", n)
		}
	}
}
