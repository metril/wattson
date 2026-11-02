package cli

import (
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metril/wattson/internal/bike"
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
