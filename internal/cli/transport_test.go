package cli

import (
	"testing"
)

func TestESPHomeRequiresKey(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{"scan", "probe", "drive"} {
		code, _, errs := runCLI(t, testDeps(nil), cmd, "--via", "esphome://proxy.local", "--addr", "C0:FF:EE:00:00:01")
		if code != 2 || errs == "" {
			t.Fatalf("%s: exit %d stderr %q, want 2 with message", cmd, code, errs)
		}
	}
}

func TestBadVia(t *testing.T) {
	t.Parallel()
	if code, _, _ := runCLI(t, testDeps(nil), "scan", "--via", "bogus"); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestESPHomeHostPort(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"esphome://proxy.local":      "proxy.local:6053",
		"esphome://10.0.0.5:7000":    "10.0.0.5:7000",
		"esphome://[fe80::1]":        "[fe80::1]:6053",
		"esphome://[fe80::1]:6053":   "[fe80::1]:6053",
		"esphome://proxy.local:6053": "proxy.local:6053",
	} {
		got, err := esphomeHostPort(in)
		if err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := esphomeHostPort("esphome://"); err == nil {
		t.Error("empty host accepted")
	}
}
