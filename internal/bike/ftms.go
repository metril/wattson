package bike

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/metril/wattson/internal/blue"
)

var (
	FTMSService         = blue.UUID16(0x1826)
	FTMSIndoorBikeData  = blue.UUID16(0x2ad2)
	FTMSControlPoint    = blue.UUID16(0x2ad9)
	FTMSResistanceRange = blue.UUID16(0x2ad6)
)

// ResistanceEncoding is the width of the Set Target Resistance parameter.
type ResistanceEncoding int

const (
	EncU8Tenths  ResistanceEncoding = iota // per spec: uint8, 0.1 resolution
	EncU16Tenths                           // Merach MRK-S28 quirk (as in QZ): uint16 LE
)

// ftmsFrame is the only way to build a control-point frame. Opcodes outside
// the allow-list (notably 0x01 Reset) panic: that is a programming error.
func ftmsFrame(op byte, params ...byte) []byte {
	switch op {
	case 0x00, 0x04, 0x05, 0x07, 0x08:
	default:
		panic(fmt.Sprintf("bike: FTMS opcode %#02x not allowed", op))
	}
	return append([]byte{op}, params...)
}

func FTMSRequestControl() []byte { return ftmsFrame(0x00) }

// FTMSSetResistance encodes level*10 (0.1 resolution). level<1 or an
// unrepresentable value is ErrLevelRange (the U8 form tops out at level 25).
func FTMSSetResistance(level int, enc ResistanceEncoding) ([]byte, error) {
	if level < 1 {
		return nil, ErrLevelRange
	}
	switch enc {
	case EncU8Tenths:
		if level > math.MaxUint8/10 {
			return nil, ErrLevelRange
		}
		return ftmsFrame(0x04, byte(level*10)), nil
	case EncU16Tenths:
		if level > math.MaxUint16/10 {
			return nil, ErrLevelRange
		}
		return ftmsFrame(0x04, binary.LittleEndian.AppendUint16(nil, uint16(level*10))...), nil
	}
	return nil, errors.New("bike: unknown resistance encoding")
}

// FTMSSetPower encodes a target power in watts as sint16 LE, saturating.
func FTMSSetPower(w int) []byte {
	w = max(math.MinInt16, min(math.MaxInt16, w))
	return ftmsFrame(0x05, binary.LittleEndian.AppendUint16(nil, uint16(int16(w)))...)
}

func FTMSStart() []byte { return ftmsFrame(0x07) }

// FTMSStop builds Stop (08 01) or Pause (08 02).
func FTMSStop(pause bool) []byte {
	if pause {
		return ftmsFrame(0x08, 0x02)
	}
	return ftmsFrame(0x08, 0x01)
}

// ParseIndoorBikeData decodes FTMS Indoor Bike Data (2AD2). Level comes from
// the Resistance Level field when present (raw value, as the bike reports it).
// Truncated input returns ErrTruncated. At is left zero.
func ParseIndoorBikeData(b []byte) (Telemetry, error) {
	if len(b) < 2 {
		return Telemetry{}, ErrTruncated
	}
	flags := binary.LittleEndian.Uint16(b)
	p := b[2:]
	var t Telemetry
	var bad bool
	take := func(n int) []byte {
		if len(p) < n {
			bad = true
			return make([]byte, n)
		}
		v := p[:n]
		p = p[n:]
		return v
	}
	u16 := func() uint16 { return binary.LittleEndian.Uint16(take(2)) }
	if flags&0x0001 == 0 {
		s := float64(u16()) / 100
		t.SpeedKmh = &s
	}
	if flags&0x0002 != 0 {
		take(2)
	}
	if flags&0x0004 != 0 {
		t.Cadence = float64(u16()) / 2
	}
	if flags&0x0008 != 0 {
		take(2)
	}
	if flags&0x0010 != 0 {
		take(3)
	}
	if flags&0x0020 != 0 {
		t.Level = int(int16(u16()))
	}
	if flags&0x0040 != 0 {
		t.PowerW = float64(int16(u16()))
	}
	if flags&0x0080 != 0 {
		take(2)
	}
	if flags&0x0100 != 0 {
		take(5)
	}
	if flags&0x0200 != 0 {
		take(1)
	}
	if flags&0x0400 != 0 {
		take(1)
	}
	if flags&0x0800 != 0 {
		take(2)
	}
	if flags&0x1000 != 0 {
		take(2)
	}
	if bad {
		return Telemetry{}, ErrTruncated
	}
	return t, nil
}

// ParseResistanceRange decodes Supported Resistance Level Range (2AD6):
// min s16, max s16, increment u16, each at 0.1 resolution.
func ParseResistanceRange(b []byte) (min, max, inc float64, err error) {
	if len(b) < 6 {
		return 0, 0, 0, ErrTruncated
	}
	return float64(int16(binary.LittleEndian.Uint16(b))) / 10,
		float64(int16(binary.LittleEndian.Uint16(b[2:]))) / 10,
		float64(binary.LittleEndian.Uint16(b[4:])) / 10, nil
}

// ControlResponse is a Control Point indication: 80 <request op> <result>.
type ControlResponse struct{ Op, Result byte }

func ParseControlResponse(b []byte) (ControlResponse, error) {
	if len(b) < 3 {
		return ControlResponse{}, ErrTruncated
	}
	if b[0] != 0x80 {
		return ControlResponse{}, fmt.Errorf("bike: not a control response (%#02x)", b[0])
	}
	return ControlResponse{Op: b[1], Result: b[2]}, nil
}
