package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"strings"

	"github.com/metril/wattson/internal/blue"
)

const (
	esphomePrefix      = "esphome://"
	esphomeDefaultPort = "6053"
)

var errKeyRequired = errors.New("--key <base64 noise psk> is required with --via esphome://")

// transportFlags are the shared --via/--key flags.
type transportFlags struct {
	via string
	key string
}

func (t *transportFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&t.via, "via", "local", "transport: local | esphome://host[:6053]")
	fs.StringVar(&t.key, "key", "", "ESPHome API noise key (base64); required for esphome://")
}

func (t *transportFlags) validate() error {
	switch {
	case t.via == "" || t.via == "local":
		t.via = "local"
		return nil
	case strings.HasPrefix(t.via, esphomePrefix):
		if _, err := esphomeHostPort(t.via); err != nil {
			return err
		}
		if t.key == "" {
			return errKeyRequired
		}
		return nil
	}
	return fmt.Errorf("bad --via %q (want local or esphome://host[:port])", t.via)
}

// label is the transport as shown in reports (never includes the key).
func (t *transportFlags) label() string {
	if strings.HasPrefix(t.via, esphomePrefix) {
		if hp, err := esphomeHostPort(t.via); err == nil {
			return esphomePrefix + hp
		}
	}
	return t.via
}

// esphomeHostPort extracts host:port (default 6053) from an esphome:// URL.
func esphomeHostPort(via string) (string, error) {
	rest := strings.TrimSuffix(strings.TrimPrefix(via, esphomePrefix), "/")
	if rest == "" {
		return "", fmt.Errorf("bad --via %q: missing host", via)
	}
	if host, port, err := net.SplitHostPort(rest); err == nil {
		if host == "" || port == "" {
			return "", fmt.Errorf("bad --via %q: missing host or port", via)
		}
		return rest, nil
	}
	return net.JoinHostPort(strings.Trim(rest, "[]"), esphomeDefaultPort), nil
}

// openTransport opens the BLE transport named by via ("local" or esphome://host[:port]).
func openTransport(ctx context.Context, via, key string) (blue.Transport, error) {
	switch {
	case via == "" || via == "local":
		return blue.NewLocal()
	case strings.HasPrefix(via, esphomePrefix):
		if key == "" {
			return nil, errKeyRequired
		}
		hp, err := esphomeHostPort(via)
		if err != nil {
			return nil, err
		}
		return blue.NewESPHome(ctx, hp, key)
	}
	return nil, fmt.Errorf("bad --via %q", via)
}
