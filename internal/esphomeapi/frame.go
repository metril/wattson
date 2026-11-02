// Package esphomeapi is an in-house client for the ESPHome native API
// (Noise-encrypted transport only).
package esphomeapi

import (
	"errors"
	"fmt"
	"io"
)

// Frame preamble bytes of the ESPHome native API.
const (
	preambleNoise     = 0x01
	preamblePlaintext = 0x00
)

// maxFrame is the largest payload a 16-bit frame length can carry.
const maxFrame = 0xFFFF

// writeFrame writes `01 LL LL <payload>` to w in a single Write.
func writeFrame(w io.Writer, payload []byte) error {
	if len(payload) > maxFrame {
		return fmt.Errorf("frame too large: %d bytes", len(payload))
	}
	buf := make([]byte, 3+len(payload))
	buf[0] = preambleNoise
	buf[1] = byte(len(payload) >> 8)
	buf[2] = byte(len(payload))
	copy(buf[3:], payload)
	_, err := w.Write(buf)
	return err
}

// readFrame reads one `01 LL LL <payload>` frame from r.
func readFrame(r io.Reader) ([]byte, error) {
	var hdr [3]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	switch hdr[0] {
	case preambleNoise:
	case preamblePlaintext:
		return nil, errors.New("device is using the plaintext protocol; enable api encryption")
	default:
		return nil, fmt.Errorf("invalid frame marker byte 0x%02x", hdr[0])
	}
	payload := make([]byte, int(hdr[1])<<8|int(hdr[2]))
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}
