//go:build linux

package blue

import (
	"errors"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
	"tinygo.org/x/bluetooth"
)

func TestAddrRoundTrip(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"AA:BB:CC:DD:EE:01", "11:22:33:44:55:66/random"} {
		a, err := ParseAddress(s)
		if err != nil {
			t.Fatal(err)
		}
		bt := toBT(a)
		if got := bt.MAC.String(); got != strings.ToUpper(strings.Split(s, "/")[0]) {
			t.Errorf("tinygo MAC %s, want %s", got, s)
		}
		if bt.IsRandom() != (a.Type == AddrRandom) {
			t.Errorf("%s: random flag lost", s)
		}
		if back := fromBT(bt); back != a {
			t.Errorf("round trip %v != %v", back, a)
		}
	}
}

func TestPropsFromFlags(t *testing.T) {
	t.Parallel()
	got := propsFromFlags([]string{"read", "write", "write-without-response", "notify", "indicate", "encrypt-read"})
	want := PropRead | PropWrite | PropWriteNoResp | PropNotify | PropIndicate
	if got != want {
		t.Fatalf("props %#x, want %#x", got, want)
	}
}

func TestLocalErrMapping(t *testing.T) {
	t.Parallel()
	denied := localErr("scan", dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied"})
	if !strings.Contains(denied.Error(), "add the container user to the host bluetooth group (see docs/hardware-runbook.md)") {
		t.Errorf("missing hint: %v", denied)
	}
	if e := localErr("enable", errors.New("dial unix /var/run/dbus/system_bus_socket: connect: permission denied")); !strings.Contains(e.Error(), "bluetooth group") {
		t.Errorf("missing hint on socket denial: %v", e)
	}
	if e := localErr("write", dbus.Error{Name: "org.bluez.Error.NotConnected"}); !errors.Is(e, ErrDisconnected) {
		t.Errorf("NotConnected should wrap ErrDisconnected: %v", e)
	}
	if e := localErr("write", errors.New("boom")); errors.Is(e, ErrDisconnected) || strings.Contains(e.Error(), "bluetooth group") {
		t.Errorf("unrelated error mis-mapped: %v", e)
	}
}

func TestGuardRecoversPanic(t *testing.T) {
	t.Parallel()
	if _, err := guard(func() (int, error) { panic("DiscoverServices exploded") }); err == nil || !strings.Contains(err.Error(), "exploded") {
		t.Fatalf("panic not converted: %v", err)
	}
}

func TestLocalErrNotConnectedMessage(t *testing.T) {
	t.Parallel()
	for _, msg := range []string{"Not connected", "GDBus.Error:org.bluez.Error.Failed: Not connected"} {
		if e := localErr("read", errors.New(msg)); !errors.Is(e, ErrDisconnected) {
			t.Errorf("%q should wrap ErrDisconnected: %v", msg, e)
		}
	}
}

// closeTwice mimics tinygo's StopScan: close without a lock, so a second call panics.
func TestStopScanTreatsDoubleClosePanicAsStopped(t *testing.T) {
	t.Parallel()
	ch := make(chan struct{})
	stop := func() (stopped bool) { // same shape as localTransport.stopScan
		defer func() {
			if recover() != nil {
				stopped = true
			}
		}()
		close(ch)
		return false
	}
	stop()
	if !stop() {
		t.Fatal("double close panic not reported as stopped")
	}
}

func TestStopScanIdleAdapter(t *testing.T) {
	t.Parallel()
	lt := &localTransport{ad: bluetooth.NewAdapter("hci-test")}
	if lt.stopScan() {
		t.Fatal("stopScan on an idle adapter must report not stopped")
	}
}
