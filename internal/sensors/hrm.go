package sensors

import (
	"errors"
	"time"

	"github.com/metril/wattson/internal/blue"
)

// Heart Rate service (0x180D) and its Heart Rate Measurement characteristic (0x2A37).
var HRService, HRMeasurement = blue.UUID16(0x180D), blue.UUID16(0x2A37)

var (
	// ErrTruncated means a Heart Rate Measurement was shorter than its flags promise.
	ErrTruncated = errors.New("sensors: truncated heart rate measurement")
	// ErrNoHRService means the connected device has no usable heart rate service.
	ErrNoHRService = errors.New("sensors: no heart rate service on device")
)

// Heart Rate Measurement flag bits (Bluetooth HRS 1.0, section 3.1.1).
const (
	hrFlagU16     = 1 << 0 // heart rate value is uint16, else uint8
	hrFlagContact = 1 << 1 // sensor contact detected (valid when supported)
	hrFlagSupport = 1 << 2 // sensor contact feature supported
	hrFlagEnergy  = 1 << 3 // energy expended (uint16 kJ) present
	hrFlagRR      = 1 << 4 // RR intervals present
)

// HRM is a parsed Heart Rate Measurement.
type HRM struct {
	BPM int // beats per minute; 0 = no valid value
	// RR holds the RR intervals in order. They are parsed for completeness and
	// not used by Wattson.
	RR []time.Duration
	// ContactSupported reports that the sensor can tell whether it touches skin;
	// Contact is then whether it does.
	ContactSupported, Contact bool
}

// ParseHRM decodes a Heart Rate Measurement notification. It never panics: input
// shorter than its flags require yields ErrTruncated. A trailing odd byte after
// the RR intervals is ignored. The energy-expended field is skipped.
func ParseHRM(b []byte) (HRM, error) {
	if len(b) < 2 {
		return HRM{}, ErrTruncated
	}
	flags := b[0]
	h := HRM{ContactSupported: flags&hrFlagSupport != 0}
	h.Contact = h.ContactSupported && flags&hrFlagContact != 0
	i := 2
	if flags&hrFlagU16 != 0 {
		if len(b) < 3 {
			return HRM{}, ErrTruncated
		}
		h.BPM = int(b[1]) | int(b[2])<<8
		i = 3
	} else {
		h.BPM = int(b[1])
	}
	if flags&hrFlagEnergy != 0 {
		if len(b) < i+2 {
			return HRM{}, ErrTruncated
		}
		i += 2
	}
	if flags&hrFlagRR != 0 {
		for ; i+2 <= len(b); i += 2 {
			v := int64(b[i]) | int64(b[i+1])<<8 // 1/1024 s units
			h.RR = append(h.RR, time.Duration(v*int64(time.Second)/1024))
		}
	}
	return h, nil
}
