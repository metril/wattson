package bike

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func TestFTMSSetResistanceU8(t *testing.T) {
	t.Parallel()
	got, err := FTMSSetResistance(5, EncU8Tenths)
	if err != nil || !bytes.Equal(got, []byte{0x04, 0x32}) {
		t.Fatalf("% x %v", got, err)
	}
}

func TestFTMSSetResistanceU16(t *testing.T) {
	t.Parallel()
	got, err := FTMSSetResistance(5, EncU16Tenths)
	if err != nil || !bytes.Equal(got, []byte{0x04, 0x32, 0x00}) {
		t.Fatalf("% x %v", got, err)
	}
	got, err = FTMSSetResistance(32, EncU16Tenths)
	if err != nil || !bytes.Equal(got, []byte{0x04, 0x40, 0x01}) {
		t.Fatalf("% x %v", got, err)
	}
}

func TestFTMSSetResistanceRange(t *testing.T) {
	t.Parallel()
	for _, enc := range []ResistanceEncoding{EncU8Tenths, EncU16Tenths} {
		for _, l := range []int{0, -1, math.MinInt, math.MaxInt} {
			if b, err := FTMSSetResistance(l, enc); !errors.Is(err, ErrLevelRange) || b != nil {
				t.Errorf("enc %d level %d: % x %v", enc, l, b, err)
			}
		}
	}
	if _, err := FTMSSetResistance(25, EncU8Tenths); err != nil {
		t.Error(err)
	}
	if _, err := FTMSSetResistance(26, EncU8Tenths); !errors.Is(err, ErrLevelRange) {
		t.Errorf("u8 overflow accepted: %v", err)
	}
	if _, err := FTMSSetResistance(5, ResistanceEncoding(99)); err == nil {
		t.Error("unknown encoding accepted")
	}
}

func TestFTMSSimpleFrames(t *testing.T) {
	t.Parallel()
	chk := func(name string, got, want []byte) {
		if !bytes.Equal(got, want) {
			t.Errorf("%s: % x want % x", name, got, want)
		}
	}
	chk("request", FTMSRequestControl(), []byte{0x00})
	chk("start", FTMSStart(), []byte{0x07})
	chk("stop", FTMSStop(false), []byte{0x08, 0x01})
	chk("pause", FTMSStop(true), []byte{0x08, 0x02})
	p200, err := FTMSSetPower(200)
	if err != nil {
		t.Fatal(err)
	}
	chk("power200", p200, []byte{0x05, 0xc8, 0x00})
	p1, err := FTMSSetPower(1)
	if err != nil {
		t.Fatal(err)
	}
	chk("power1", p1, []byte{0x05, 0x01, 0x00})
	phi, err := FTMSSetPower(1 << 40)
	if err != nil {
		t.Fatal(err)
	}
	chk("power-sat-hi", phi, []byte{0x05, 0xff, 0x7f})
}

func TestFTMSSetPowerRejectsNonPositive(t *testing.T) {
	t.Parallel()
	for _, w := range []int{0, -1, math.MinInt} {
		if b, err := FTMSSetPower(w); !errors.Is(err, ErrPowerRange) || b != nil {
			t.Errorf("w %d: % x %v", w, b, err)
		}
	}
}

func TestFTMSFrameRejectsReset(t *testing.T) {
	t.Parallel()
	for _, op := range []byte{0x01, 0x02, 0x03, 0x06, 0x09, 0x80, 0xff} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("op %#x did not panic", op)
				}
			}()
			ftmsFrame(op)
		}()
	}
	for _, op := range []byte{0x00, 0x04, 0x05, 0x07, 0x08} {
		if f := ftmsFrame(op, 1); f[0] != op {
			t.Errorf("op %#x", op)
		}
	}
}

func TestFTMSUUIDs(t *testing.T) {
	t.Parallel()
	if FTMSService.String() != "1826" || FTMSIndoorBikeData.String() != "2ad2" || FTMSControlPoint.String() != "2ad9" || FTMSResistanceRange.String() != "2ad6" {
		t.Fatal("uuid mismatch")
	}
}

func TestParseIndoorBikeData(t *testing.T) {
	t.Parallel()
	tel, err := ParseIndoorBikeData([]byte{0x44, 0x00, 0xe8, 0x03, 0xa0, 0x00, 0xc8, 0x00})
	if err != nil {
		t.Fatal(err)
	}
	if tel.SpeedKmh == nil || *tel.SpeedKmh != 10.0 || tel.Cadence != 80.0 || tel.PowerW != 200 || tel.Level != 0 || !tel.At.IsZero() {
		t.Fatalf("%+v", tel)
	}
}

func TestParseIndoorBikeDataAllFields(t *testing.T) {
	t.Parallel()
	// flags: bits 1..12 set, bit0 clear (speed present) => 0x1ffe
	b := []byte{0xfe, 0x1f,
		0xe8, 0x03, // speed 10.00
		0x01, 0x00, // avg speed
		0xa0, 0x00, // cadence 80
		0x01, 0x00, // avg cadence
		0x01, 0x02, 0x03, // distance
		0x07, 0x00, // resistance 7
		0xc8, 0x00, // power 200
		0x01, 0x00, // avg power
		0x01, 0x00, 0x01, 0x00, 0x01, // energy
		0x90,       // HR
		0x01,       // MET
		0x01, 0x00, // elapsed
		0x01, 0x00, // remaining
	}
	tel, err := ParseIndoorBikeData(b)
	if err != nil || tel.Level != 7 || tel.Cadence != 80 || tel.PowerW != 200 || tel.SpeedKmh == nil || *tel.SpeedKmh != 10 {
		t.Fatalf("%+v %v", tel, err)
	}
	// every truncation must error, none may panic
	for n := 0; n < len(b); n++ {
		if _, err := ParseIndoorBikeData(b[:n]); err == nil {
			t.Errorf("len %d accepted", n)
		}
	}
}

func TestParseIndoorBikeDataNoSpeed(t *testing.T) {
	t.Parallel()
	// bit0 set => speed absent; bit6 power only
	tel, err := ParseIndoorBikeData([]byte{0x41, 0x00, 0x2c, 0x01})
	if err != nil || tel.SpeedKmh != nil || tel.PowerW != 300 {
		t.Fatalf("%+v %v", tel, err)
	}
	// negative power
	tel, err = ParseIndoorBikeData([]byte{0x41, 0x00, 0xff, 0xff})
	if err != nil || tel.PowerW != -1 {
		t.Fatalf("%+v %v", tel, err)
	}
}

func FuzzParseIndoorBikeData(f *testing.F) {
	f.Add([]byte{0x44, 0x00, 0xe8, 0x03, 0xa0, 0x00, 0xc8, 0x00})
	f.Add([]byte{0xfe, 0x1f})
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = ParseIndoorBikeData(b) })
}

func FuzzParsersNoPanic(f *testing.F) {
	f.Add([]byte{0x0a, 0x00, 0x40, 0x01, 0x0a, 0x00})
	f.Add([]byte{0x80, 0x04, 0x01})
	f.Add(make([]byte, 15))
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _, _, _ = ParseResistanceRange(b)
		_, _ = ParseControlResponse(b)
		_, _ = ParseFitPlus(b)
	})
}

func TestParseResistanceRange(t *testing.T) {
	t.Parallel()
	mn, mx, inc, err := ParseResistanceRange([]byte{0x0a, 0x00, 0x40, 0x01, 0x0a, 0x00})
	if err != nil || mn != 1 || mx != 32 || inc != 1 {
		t.Fatalf("%v %v %v %v", mn, mx, inc, err)
	}
	for n := 0; n < 6; n++ {
		if _, _, _, err := ParseResistanceRange(make([]byte, n)); err == nil {
			t.Errorf("len %d accepted", n)
		}
	}
}

func TestParseControlResponse(t *testing.T) {
	t.Parallel()
	r, err := ParseControlResponse([]byte{0x80, 0x04, 0x01})
	if err != nil || r != (ControlResponse{Op: 4, Result: 1}) {
		t.Fatalf("%+v %v", r, err)
	}
	for _, b := range [][]byte{nil, {0x80}, {0x80, 0x04}, {0x81, 0x04, 0x01}} {
		if _, err := ParseControlResponse(b); err == nil {
			t.Errorf("% x accepted", b)
		}
	}
}
