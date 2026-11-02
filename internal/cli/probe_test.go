package cli

import (
	"context"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/blue/bluetest"
)

func TestProbePrintsTreeAndHex(t *testing.T) {
	t.Parallel()
	f := fitPlusEcho()
	stop := make(chan struct{})
	var stopped atomic.Bool
	go func() {
		for !stopped.Load() {
			f.Notify(testAddr, bike.FitPlusNotify, make([]byte, 15))
			time.Sleep(time.Millisecond)
		}
		close(stop)
	}()
	hexlog := t.TempDir() + "/p.hexlog"
	code, out, errs := runCLI(t, testDeps(f), "probe", "--addr", testAddr.String(), "--duration", "40ms", "--out", hexlog)
	stopped.Store(true)
	<-stop
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"fff0", "fff1: notify", "fff2: write", "protocol: fitplus", "device: MRK-S29R2"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}
	hexLine := regexp.MustCompile(`(?m)^\d+ fff1 0{30}$`)
	if !hexLine.MatchString(out) {
		t.Errorf("no hex line in stdout:\n%s", out)
	}
	b, err := os.ReadFile(hexlog)
	if err != nil || !hexLine.Match(b) || strings.Contains(string(b), "protocol") {
		t.Fatalf("hexlog %q err %v", b, err)
	}
}

func TestProbeSelftest(t *testing.T) {
	t.Parallel()
	code, out, errs := runCLI(t, testDeps(fitPlusEcho()), "probe", "--addr", testAddr.String(), "--duration", "5ms", "--selftest")
	if code != 0 || !strings.Contains(out, "selftest: PASS") {
		t.Fatalf("exit %d out %s err %s", code, out, errs)
	}
}

func TestProbeSelftestFailsWithoutNotification(t *testing.T) {
	t.Parallel()
	code, out, _ := runCLI(t, testDeps(newFitPlusFake(nil)), "probe", "--addr", testAddr.String(), "--duration", "5ms", "--selftest")
	if code != 1 || !strings.Contains(out, "selftest: FAIL") {
		t.Fatalf("exit %d out %s", code, out)
	}
}

func streamIndoorBikeData(f *bluetest.Fake) (stop func()) {
	var stopped atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !stopped.Load() {
			f.Notify(testAddr, bike.FTMSIndoorBikeData, []byte{0x65, 0x00, 120, 0, 1, 0, 80, 0})
			time.Sleep(time.Millisecond)
		}
	}()
	return func() { stopped.Store(true); <-done }
}

func TestProbeSelftestFTMSNeedsControlPointAnswer(t *testing.T) {
	t.Parallel()
	// Streams 2AD2 continuously but ignores Request Control: must FAIL.
	f := newFTMSFake(false)
	stop := streamIndoorBikeData(f)
	code, out, _ := runCLI(t, testDeps(f), "probe", "--addr", testAddr.String(), "--duration", "5ms", "--selftest")
	stop()
	if code != 1 || !strings.Contains(out, "selftest: FAIL") || !strings.Contains(out, "protocol: ftms") {
		t.Fatalf("exit %d out %s", code, out)
	}
	// Answers 80 00 01: must PASS.
	f = newFTMSFake(true)
	stop = streamIndoorBikeData(f)
	code, out, _ = runCLI(t, testDeps(f), "probe", "--addr", testAddr.String(), "--duration", "5ms", "--selftest")
	stop()
	if code != 0 || !strings.Contains(out, "selftest: PASS") {
		t.Fatalf("exit %d out %s", code, out)
	}
}

func TestProbeSelftestFitPlusIgnoresOtherNotifications(t *testing.T) {
	t.Parallel()
	// Only fff1 may answer; a silent FitPlus device that merely has other
	// traffic elsewhere still fails (covered by the nil-report fake).
	code, out, _ := runCLI(t, testDeps(newFitPlusFake(nil)), "probe", "--addr", testAddr.String(), "--duration", "5ms", "--selftest")
	if code != 1 || !strings.Contains(out, "no answer on fff1") {
		t.Fatalf("exit %d out %s", code, out)
	}
}

func TestProbeOutNotTruncatedWhenConnectFails(t *testing.T) {
	t.Parallel()
	hexlog := t.TempDir() + "/keep.hexlog"
	if err := os.WriteFile(hexlog, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := "C0:FF:EE:00:00:99" // not on the fake: Connect blocks until its ctx ends
	d := testDeps(fitPlusEcho())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var out, errb strings.Builder
	code := run(ctx, d, []string{"probe", "--addr", other, "--out", hexlog}, &out, &errb)
	b, _ := os.ReadFile(hexlog)
	if code != 1 || string(b) != "old\n" {
		t.Fatalf("exit %d, file %q", code, b)
	}
}
