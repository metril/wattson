package cli

import (
	"strings"
	"testing"

	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/blue/bluetest"
)

func TestScanListsFakeDevice(t *testing.T) {
	t.Parallel()
	other := blue.Address{MAC: [6]byte{1, 2, 3, 4, 5, 6}}
	f := bluetest.NewFake(
		&bluetest.Device{Adv: blue.Advertisement{Addr: testAddr, Name: "MRK-S29R2", RSSI: -60, Services: []blue.UUID{blue.UUID16(0xfff0)}}},
		&bluetest.Device{Adv: blue.Advertisement{Addr: other, Name: "Other", RSSI: -40}},
	)
	code, out, errs := runCLI(t, testDeps(f), "scan", "--duration", "30ms")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "ADDR") {
		t.Fatalf("output:\n%s", out)
	}
	if !strings.Contains(lines[1], "Other") || !strings.Contains(lines[2], "C0:FF:EE:00:00:01/random") ||
		!strings.Contains(lines[2], "MRK-S29R2") || !strings.Contains(lines[2], "fff0") || !strings.Contains(lines[2], "-60") {
		t.Fatalf("want RSSI-sorted rows, got:\n%s", out)
	}
	_, out, _ = runCLI(t, testDeps(f), "scan", "--duration", "30ms", "--name-prefix", "mrk-")
	if strings.Contains(out, "Other") || !strings.Contains(out, "MRK-S29R2") {
		t.Fatalf("prefix filter failed:\n%s", out)
	}
}
