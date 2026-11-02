package sensors

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestParseHRM(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []byte
		want HRM
		err  error
	}{
		{"u8", []byte{0x00, 0x48}, HRM{BPM: 72}, nil},
		{"u16", []byte{0x01, 0x2c, 0x01}, HRM{BPM: 300}, nil},
		{"rr", []byte{0x10, 0x3c, 0x00, 0x04}, HRM{BPM: 60, RR: []time.Duration{time.Second}}, nil},
		{"energy+rr", []byte{0x18, 0x3c, 0x10, 0x00, 0x00, 0x02, 0x00, 0x02}, HRM{BPM: 60, RR: []time.Duration{500 * time.Millisecond, 500 * time.Millisecond}}, nil},
		{"u16 truncated", []byte{0x01, 0x2c}, HRM{}, ErrTruncated},
		{"empty", nil, HRM{}, ErrTruncated},
		{"one byte", []byte{0x00}, HRM{}, ErrTruncated},
		{"zero bpm", []byte{0x00, 0x00}, HRM{BPM: 0}, nil},
		{"energy truncated", []byte{0x08, 0x3c, 0x10}, HRM{}, ErrTruncated},
		{"odd trailing RR byte ignored", []byte{0x10, 0x3c, 0x00, 0x04, 0x07}, HRM{BPM: 60, RR: []time.Duration{time.Second}}, nil},
		{"contact detected", []byte{0x06, 0x48}, HRM{BPM: 72, ContactSupported: true, Contact: true}, nil},
		{"contact lost", []byte{0x04, 0x48}, HRM{BPM: 72, ContactSupported: true}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseHRM(tc.in)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if err == nil && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func FuzzParseHRM(f *testing.F) {
	for _, s := range [][]byte{{0, 72}, {1, 0x2c, 1}, {0x18, 60, 16, 0, 0, 2, 0, 2}, {0x1f}, nil} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		h, err := ParseHRM(b)
		if err != nil {
			return
		}
		if h.BPM < 0 || h.BPM > 0xffff {
			t.Fatalf("BPM %d out of range", h.BPM)
		}
		if len(h.RR) > (len(b)-2)/2 {
			t.Fatalf("%d RR intervals from %d bytes", len(h.RR), len(b))
		}
	})
}
