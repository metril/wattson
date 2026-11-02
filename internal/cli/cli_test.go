package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunUnknownSubcommand(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if code := Run([]string{"bogus"}, &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "usage: wattson") {
		t.Fatalf("stderr = %q, want usage", errb.String())
	}
}

func TestRunNoArgs(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if code := Run(nil, &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "usage: wattson") {
		t.Fatalf("stderr = %q, want usage", errb.String())
	}
}

func TestRunVersion(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if code := Run([]string{"version"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "wattson dev") {
		t.Fatalf("stdout = %q, want %q", out.String(), "wattson dev")
	}
}
