package bike

import (
	"encoding/binary"

	"github.com/metril/wattson/internal/blue"
)

var (
	FitPlusService = blue.UUID16(0xfff0)
	FitPlusNotify  = blue.UUID16(0xfff1)
	FitPlusWrite   = blue.UUID16(0xfff2)
)

// fitPlusLevels is {XX, CC} per level (index = level-1). Frame is
// 02 44 05 LL XX CC 03 with CC = 0x44^0x05^LL^XX.
var fitPlusLevels = [32][2]byte{
	{0xf9, 0xb9}, {0xf9, 0xba}, {0xfa, 0xb8}, {0xfb, 0xbe}, {0xfc, 0xb8}, {0xfd, 0xba}, {0xfe, 0xb8}, {0xff, 0xb6},
	{0x00, 0x48}, {0x00, 0x4b}, {0x01, 0x4b}, {0x03, 0x4e}, {0x03, 0x4f}, {0x05, 0x4a}, {0x05, 0x4b}, {0x06, 0x57},
	{0x07, 0x57}, {0x08, 0x5b}, {0x09, 0x5b}, {0x0a, 0x5f}, {0x0b, 0x5f}, {0x0c, 0x5b}, {0x0d, 0x5b}, {0x0e, 0x57},
	{0x00, 0x58}, {0x00, 0x5b}, {0x00, 0x5a}, {0x00, 0x5d}, {0x00, 0x5c}, {0x00, 0x5f}, {0x00, 0x5e}, {0x00, 0x61},
}

// FitPlusInit is the handshake written once after subscribing.
func FitPlusInit() []byte { return []byte{0xaa, 0x01, 0x00, 0x01, 0x55} }

// FitPlusKeepalive alternates two frames by index parity.
func FitPlusKeepalive(i int) []byte {
	if i&1 == 0 {
		return []byte{0x02, 0x42, 0x42, 0x03}
	}
	return []byte{0x02, 0x43, 0x01, 0x42, 0x03}
}

// FitPlusSetLevel builds the set-level frame for level 1..32; anything else
// (notably 0) is ErrLevelRange.
func FitPlusSetLevel(level int) ([]byte, error) {
	if level < 1 || level > len(fitPlusLevels) {
		return nil, ErrLevelRange
	}
	r := fitPlusLevels[level-1]
	return []byte{0x02, 0x44, 0x05, byte(level), r[0], r[1], 0x03}, nil
}

// ParseFitPlus decodes a 15-byte telemetry notification; other lengths are
// not telemetry and return false. At is left zero.
func ParseFitPlus(b []byte) (Telemetry, bool) {
	if len(b) != 15 {
		return Telemetry{}, false
	}
	return Telemetry{
		Level:   int(b[5]),
		Cadence: float64(b[6]),
		PowerW:  float64(binary.LittleEndian.Uint16(b[9:11])) / 10,
	}, true
}
