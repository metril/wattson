// Package blue defines a transport-agnostic BLE central API.
package blue

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound         = errors.New("blue: device not found")
	ErrDisconnected     = errors.New("blue: disconnected")
	ErrNoSlots          = errors.New("blue: no free connection slots")
	ErrNoCharacteristic = errors.New("blue: no such characteristic")
	// ErrProxyHeld means an ESPHome Bluetooth proxy is working for another API
	// client: it allows one advertisement subscription at a time and answers
	// only that client, so this one gets nothing back.
	ErrProxyHeld = errors.New("blue: proxy bluetooth subscription held by another client")
)

// UUID is a 128-bit UUID in big-endian canonical byte order.
type UUID [16]byte

var sigBase = UUID{0, 0, 0, 0, 0, 0, 0x10, 0, 0x80, 0, 0, 0x80, 0x5f, 0x9b, 0x34, 0xfb}

// UUID16 expands a 16-bit SIG UUID onto the Bluetooth base UUID.
func UUID16(v uint16) UUID {
	u := sigBase
	u[2], u[3] = byte(v>>8), byte(v)
	return u
}

// ParseUUID accepts a 4-hex short form, 8-hex 32-bit form, or the full 128-bit form.
func ParseUUID(s string) (UUID, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch len(s) {
	case 4, 8:
		b, err := hex.DecodeString(s)
		if err != nil {
			return UUID{}, fmt.Errorf("blue: bad uuid %q: %w", s, err)
		}
		u := sigBase
		copy(u[4-len(b):4], b)
		return u, nil
	case 32:
	case 36:
		if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
			return UUID{}, fmt.Errorf("blue: bad uuid %q", s)
		}
		s = strings.ReplaceAll(s, "-", "")
	default:
		return UUID{}, fmt.Errorf("blue: bad uuid %q", s)
	}
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		return UUID{}, fmt.Errorf("blue: bad uuid %q", s)
	}
	var u UUID
	copy(u[:], b)
	return u, nil
}

// String returns the short form for 16-bit SIG UUIDs, the canonical form otherwise.
func (u UUID) String() string {
	if u[0] == 0 && u[1] == 0 && [12]byte(u[4:]) == [12]byte(sigBase[4:]) {
		return hex.EncodeToString(u[2:4])
	}
	h := hex.EncodeToString(u[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

type AddrType uint8

const (
	AddrPublic AddrType = 0
	AddrRandom AddrType = 1
)

type Address struct {
	MAC  [6]byte
	Type AddrType
}

// ParseAddress parses "AA:BB:CC:DD:EE:FF" with an optional "/random" or "/public" suffix.
func ParseAddress(s string) (Address, error) {
	var a Address
	mac, suffix, hasSuffix := strings.Cut(strings.TrimSpace(s), "/")
	if hasSuffix {
		switch strings.ToLower(suffix) {
		case "random":
			a.Type = AddrRandom
		case "public":
		default:
			return Address{}, fmt.Errorf("blue: bad address type in %q", s)
		}
	}
	parts := strings.Split(mac, ":")
	if len(parts) != 6 {
		return Address{}, fmt.Errorf("blue: bad address %q", s)
	}
	for i, p := range parts {
		b, err := hex.DecodeString(p)
		if err != nil || len(b) != 1 {
			return Address{}, fmt.Errorf("blue: bad address %q", s)
		}
		a.MAC[i] = b[0]
	}
	return a, nil
}

func (a Address) String() string {
	s := strings.ToUpper(fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", a.MAC[0], a.MAC[1], a.MAC[2], a.MAC[3], a.MAC[4], a.MAC[5]))
	if a.Type == AddrRandom {
		s += "/random"
	}
	return s
}

// Uint64 packs the MAC with the first octet most significant (ESPHome encoding).
func (a Address) Uint64() uint64 {
	var v uint64
	for _, b := range a.MAC {
		v = v<<8 | uint64(b)
	}
	return v
}

type Props uint8

const (
	PropRead        Props = 0x02
	PropWriteNoResp Props = 0x04
	PropWrite       Props = 0x08
	PropNotify      Props = 0x10
	PropIndicate    Props = 0x20
)

type Characteristic struct {
	UUID   UUID
	Handle uint16
	Props  Props
}

type Service struct {
	UUID  UUID
	Chars []Characteristic
}

type Advertisement struct {
	Addr     Address
	Name     string
	RSSI     int
	Services []UUID
}

type Transport interface {
	// Scan blocks until ctx is done, calling fn per advertisement; returns nil on ctx cancel.
	Scan(ctx context.Context, fn func(Advertisement)) error
	// Connect discovers services before returning.
	Connect(ctx context.Context, addr Address) (Conn, error)
	Close() error
}

type Conn interface {
	Services() []Service
	Read(ctx context.Context, ch UUID) ([]byte, error)
	Write(ctx context.Context, ch UUID, data []byte, withResponse bool) error
	// Subscribe enables notify or indicate, whichever the characteristic supports.
	Subscribe(ctx context.Context, ch UUID, fn func([]byte)) error
	// Done is closed on local Close or remote disconnect.
	Done() <-chan struct{}
	// Err is nil until Done is closed; then nil after a local Close and
	// wrapping ErrDisconnected otherwise.
	Err() error
	Close() error
}

// ParseAdvertisingData extracts the local name (AD 0x08/0x09) and service UUIDs
// (AD 0x02/0x03 16-bit, 0x06/0x07 128-bit) from raw advertising data.
func ParseAdvertisingData(b []byte) (name string, services []UUID) {
	for len(b) > 0 {
		n := int(b[0])
		if n == 0 || n >= len(b) {
			break
		}
		typ, data := b[1], b[2:1+n]
		b = b[1+n:]
		switch typ {
		case 0x02, 0x03:
			for i := 0; i+2 <= len(data); i += 2 {
				services = append(services, UUID16(uint16(data[i])|uint16(data[i+1])<<8))
			}
		case 0x06, 0x07:
			for i := 0; i+16 <= len(data); i += 16 {
				var u UUID
				for j := 0; j < 16; j++ {
					u[j] = data[i+15-j]
				}
				services = append(services, u)
			}
		case 0x08, 0x09:
			name = string(data)
		}
	}
	return name, services
}

// ProxyHeldError wraps ErrProxyHeld for op on the named proxy with a hint on
// what to do. Wattson never takes the subscription back: that would start a
// fight with the other client.
func ProxyHeldError(op, proxyName string) error {
	return fmt.Errorf("%s: %w: %s did not answer; another client (Home Assistant's ESPHome integration, for one) is subscribed to its Bluetooth, and ESPHome allows only one. Disconnect it or give Wattson a dedicated proxy", op, ErrProxyHeld, proxyName)
}

// ProxyStatus is a snapshot of a Bluetooth proxy.
type ProxyStatus struct {
	Name, MAC, Version string // device name, MAC address and ESPHome version
	Flags              uint32 // bluetooth_proxy feature flags
	// Subscribed is false when the proxy did not answer: another API client
	// holds its Bluetooth subscription (see ErrProxyHeld) or it is unresponsive.
	Subscribed bool
	Free       int       // free connection slots
	Limit      int       // total connection slots
	Allocated  []Address // devices holding a slot
}

// Proxy is implemented by transports that reach Bluetooth through a remote proxy.
type Proxy interface {
	// Done is closed when the link to the proxy is gone.
	Done() <-chan struct{}
	// ProxyStatus queries the proxy; the caller bounds ctx. When ctx expires
	// while the link is alive it returns Subscribed false and a nil error; a dead
	// link gives an error wrapping ErrDisconnected.
	ProxyStatus(ctx context.Context) (ProxyStatus, error)
}
