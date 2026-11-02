package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/blue/bluetest"
)

func driveArgs(extra ...string) []string {
	return append([]string{"drive", "--addr", testAddr.String(), "--dwell", "20ms"}, extra...)
}

func TestDriveSweepsAndReports(t *testing.T) {
	t.Parallel()
	f := fitPlusEcho()
	rep := t.TempDir() + "/sub/s29r2.md"
	code, out, errs := runCLI(t, testDeps(f), driveArgs("--max", "3", "--report", rep)...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	got := levelWrites(f)
	if len(got) < 5 || !reflect.DeepEqual(got[:5], []int{1, 2, 3, 2, 1}) || got[len(got)-1] != 1 {
		t.Fatalf("level writes = %v, want 1,2,3,2,1 then close at min", got)
	}
	if !strings.Contains(out, "confirmed 5/5") {
		t.Fatalf("stdout:\n%s", out)
	}
	b, err := os.ReadFile(rep)
	if err != nil {
		t.Fatal(err)
	}
	md := string(b)
	rows := regexp.MustCompile(`(?m)^\| \d+ \| \d+ \| yes \| 60\.0 \| 80\.0 \|$`).FindAllString(md, -1)
	if len(rows) != 5 {
		t.Fatalf("want 5 confirmed rows, got %d:\n%s", len(rows), md)
	}
	for _, want := range []string{
		"Generated: 2026-11-02T10:00:00Z", "- Device: MRK-S29R2", "- Address: C0:FF:EE:00:00:01 (random)",
		"- Transport: local", "- Protocol: fitplus", "- Sweep: complete", "- fff0 (FitPlus vendor service)",
		"  - fff1: notify", "Confirmed", "## Mismatches\n\nNone.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report missing %q:\n%s", want, md)
		}
	}
}

func TestDriveDisconnectExitsCleanly(t *testing.T) {
	t.Parallel()
	f := fitPlusEcho()
	type res struct {
		code int
		errs string
	}
	done := make(chan res, 1)
	go func() {
		code, _, errs := runCLI(t, testDeps(f), driveArgs()...) // 31 steps: far longer than the test
		done <- res{code, errs}
	}()
	for len(levelWrites(f)) < 2 {
		time.Sleep(time.Millisecond)
	}
	f.DropLink(testAddr)
	select {
	case r := <-done:
		if r.code != 1 || !strings.Contains(r.errs, "bike disconnected at level") {
			t.Fatalf("exit %d stderr %q", r.code, r.errs)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("drive did not exit within 500ms of link drop")
	}
}

func TestDriveInterruptDropsToMin(t *testing.T) {
	t.Parallel()
	f := fitPlusEcho()
	ctx, cancel := context.WithCancel(context.Background())
	type res struct {
		code int
		errs string
	}
	done := make(chan res, 1)
	go func() {
		var out, errb strings.Builder
		code := run(ctx, testDeps(f), driveArgs(), &out, &errb)
		done <- res{code, errb.String()}
	}()
	for len(levelWrites(f)) < 3 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case r := <-done:
		if r.code != 130 || !strings.Contains(r.errs, "interrupted at level") {
			t.Fatalf("exit %d stderr %q", r.code, r.errs)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("drive did not exit after cancel")
	}
	if w := levelWrites(f); w[len(w)-1] != 1 {
		t.Fatalf("last level written = %d, want min 1 (%v)", w[len(w)-1], w)
	}
}

func TestDriveNeverSendsZero(t *testing.T) {
	t.Parallel()
	f := fitPlusEcho()
	code, _, errs := runCLI(t, testDeps(f), driveArgs("--min", "0", "--max", "2")...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	got := levelWrites(f)
	if len(got) == 0 || got[0] != 1 {
		t.Fatalf("first level = %v, want 1", got)
	}
	for _, l := range got {
		if l == 0 {
			t.Fatalf("level 0 sent: %v", got)
		}
	}
}

func TestDriveReportsMismatch(t *testing.T) {
	t.Parallel()
	f := newFitPlusFake(func(int) int { return 1 }) // bike stuck reporting level 1
	rep := t.TempDir() + "/r.md"
	code, _, errs := runCLI(t, testDeps(f), driveArgs("--max", "2", "--report", rep)...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	b, _ := os.ReadFile(rep)
	if !strings.Contains(string(b), "- sent 2, bike reported 1") {
		t.Fatalf("report:\n%s", b)
	}
}

func TestDriveRejectsBadFlags(t *testing.T) {
	t.Parallel()
	for _, a := range [][]string{
		{"drive"}, // no --addr
		{"drive", "--addr", "nope"},
		driveArgs("--proto", "x"),
		driveArgs("--ftms-enc", "x"),
		driveArgs("--max", "0"),
	} {
		if code, _, _ := runCLI(t, testDeps(fitPlusEcho()), a...); code != 2 {
			t.Errorf("%v: exit %d, want 2", a, code)
		}
	}
}

func TestSummarizeModeOfLastHalf(t *testing.T) {
	t.Parallel()
	t0 := time.Unix(0, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	s := []bike.Telemetry{
		{Level: 1, Cadence: 10, PowerW: 10, At: at(10)}, // first half: ignored
		{Level: 2, Cadence: 60, PowerW: 100, At: at(60)},
		{Level: 2, Cadence: 70, PowerW: 120, At: at(70)},
		{Level: 3, Cadence: 80, PowerW: 140, At: at(80)},
		{Level: 9, At: at(500)}, // outside window
	}
	r := summarize(2, s, at(0), at(50), at(100))
	if r.Reported != 2 || r.Samples != 3 || r.AvgCadence != 70 || r.AvgPower != 120 {
		t.Fatalf("%+v", r)
	}
	if r := summarize(2, nil, at(0), at(50), at(100)); r.Reported != 0 || r.confirmed() {
		t.Fatalf("empty window: %+v", r)
	}
}

func flaky(f *bluetest.Fake, t0 *flakyTransport) deps {
	t0.Transport = f
	return testDeps(t0)
}

func TestDriveWriteErrDisconnectedIsDropNotFailure(t *testing.T) {
	t.Parallel()
	f := fitPlusEcho()
	ft := &flakyTransport{failFrom: 2, failOnly: true, err: fmt.Errorf("write: %w", blue.ErrDisconnected)}
	code, _, errs := runCLI(t, flaky(f, ft), driveArgs("--max", "3")...)
	if code != 1 || !strings.Contains(errs, "bike disconnected at level 1") || strings.Contains(errs, "command failed") {
		t.Fatalf("exit %d stderr %q", code, errs)
	}
}

func TestDriveFailedWriteReportsReturned(t *testing.T) {
	t.Parallel()
	f := fitPlusEcho()
	ft := &flakyTransport{failFrom: 2, failOnly: true, err: errors.New("boom")}
	code, _, errs := runCLI(t, flaky(f, ft), driveArgs("--max", "3")...)
	if code != 1 || !strings.Contains(errs, "set level 2: boom") || !strings.Contains(errs, "bike returned to level 1") {
		t.Fatalf("exit %d stderr %q", code, errs)
	}
}

func TestDriveCloseFailureIsNotClaimedAsSafe(t *testing.T) {
	t.Parallel()
	f := fitPlusEcho()
	ft := &flakyTransport{err: errors.New("radio dead")}
	ctx, cancel := context.WithCancel(context.Background())
	type res struct {
		code int
		errs string
	}
	done := make(chan res, 1)
	go func() {
		var out, errb strings.Builder
		code := run(ctx, flaky(f, ft), driveArgs(), &out, &errb)
		done <- res{code, errb.String()}
	}()
	for ft.levelWrote.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	ft.armed.Store(true) // from now on the min-level write in Close fails
	cancel()
	select {
	case r := <-done:
		if r.code != 130 || !strings.Contains(r.errs, "failed to return bike to level 1: ") ||
			!strings.Contains(r.errs, "radio dead") || strings.Contains(r.errs, "bike returned to level") {
			t.Fatalf("exit %d stderr %q", r.code, r.errs)
		}
	case <-time.After(time.Second):
		t.Fatal("drive did not exit")
	}
}

func TestDriveFTMS(t *testing.T) {
	t.Parallel()
	f := newFTMSFake(true)
	rep := t.TempDir() + "/ftms.md"
	code, out, errs := runCLI(t, testDeps(f), driveArgs("--proto", "ftms", "--ftms-enc", "u8", "--max", "3", "--report", rep)...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var got [][]byte
	for _, w := range f.Writes(testAddr) {
		got = append(got, w)
	}
	// request control, then resistance 1,2,3,2,1 as u8 tenths, then close at min.
	want := [][]byte{{0x00}, {0x04, 10}, {0x04, 20}, {0x04, 30}, {0x04, 20}, {0x04, 10}, {0x04, 10}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("writes = %x, want %x", got, want)
	}
	if !strings.Contains(out, "protocol ftms") || !strings.Contains(out, "confirmed 5/5") {
		t.Fatalf("stdout:\n%s", out)
	}
	b, _ := os.ReadFile(rep)
	if n := len(regexp.MustCompile(`(?m)^\| \d+ \| \d+ \| yes \| 60\.0 \| 80\.0 \|$`).FindAllString(string(b), -1)); n != 5 || !strings.Contains(string(b), "- Protocol: ftms") {
		t.Fatalf("report:\n%s", b)
	}
}
