package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/metril/wattson/internal/blue"
)

var serviceNames = map[string]string{
	"1800": "Generic Access",
	"1801": "Generic Attribute",
	"180a": "Device Information",
	"180f": "Battery",
	"1826": "Fitness Machine",
	"fff0": "FitPlus vendor service",
}

// writeTree prints services and characteristics as a nested list (valid in
// both a terminal and markdown).
func writeTree(w io.Writer, svcs []blue.Service) {
	for _, s := range svcs {
		if n, ok := serviceNames[s.UUID.String()]; ok {
			fmt.Fprintf(w, "- %s (%s)\n", s.UUID, n)
		} else {
			fmt.Fprintf(w, "- %s\n", s.UUID)
		}
		for _, c := range s.Chars {
			fmt.Fprintf(w, "  - %s: %s\n", c.UUID, propsString(c.Props))
		}
	}
}

func propsString(p blue.Props) string {
	var out []string
	for _, x := range []struct {
		bit  blue.Props
		name string
	}{
		{blue.PropRead, "read"}, {blue.PropWrite, "write"}, {blue.PropWriteNoResp, "write-no-resp"},
		{blue.PropNotify, "notify"}, {blue.PropIndicate, "indicate"},
	} {
		if p&x.bit != 0 {
			out = append(out, x.name)
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ", ")
}

// levelRow is one sweep step.
type levelRow struct {
	Sent       int
	Reported   int // 0 = no telemetry received
	AvgCadence float64
	AvgPower   float64
	Samples    int
}

func (r levelRow) confirmed() bool { return r.Reported == r.Sent }

func (r levelRow) reportedStr() string {
	if r.Reported == 0 {
		return "-"
	}
	return fmt.Sprint(r.Reported)
}

func (r levelRow) cadenceStr() string {
	if r.Samples == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f", r.AvgCadence)
}

func (r levelRow) powerStr() string {
	if r.Samples == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f", r.AvgPower)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

type report struct {
	At        time.Time
	Name      string
	Addr      blue.Address
	Transport string
	Protocol  string
	MaxLevel  int
	Services  []blue.Service
	Rows      []levelRow
	Status    string // "complete" or a reason for an incomplete sweep
}

// writeMarkdown renders the report; output depends only on r.
func (r report) writeMarkdown(w io.Writer) {
	name := r.Name
	if name == "" {
		name = "(unknown)"
	}
	fmt.Fprintf(w, "# Wattson drive report\n\nGenerated: %s\n\n", r.At.UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "- Device: %s\n- Address: %s (%s)\n- Transport: %s\n- Protocol: %s\n- Max level: %d\n- Sweep: %s\n\n",
		name, macString(r.Addr), addrTypeName(r.Addr.Type), r.Transport, r.Protocol, r.MaxLevel, r.Status)
	fmt.Fprint(w, "## Services\n\n")
	writeTree(w, r.Services)
	fmt.Fprint(w, "\n## Levels\n\n")
	fmt.Fprint(w, "| Sent | Reported | Confirmed | Avg cadence (rpm) | Avg power (W) |\n|---:|---:|:---:|---:|---:|\n")
	for _, row := range r.Rows {
		fmt.Fprintf(w, "| %d | %s | %s | %s | %s |\n", row.Sent, row.reportedStr(), yesNo(row.confirmed()), row.cadenceStr(), row.powerStr())
	}
	fmt.Fprint(w, "\n## Mismatches\n\n")
	n := 0
	for _, row := range r.Rows {
		if !row.confirmed() {
			fmt.Fprintf(w, "- sent %d, bike reported %s\n", row.Sent, row.reportedStr())
			n++
		}
	}
	if n == 0 {
		fmt.Fprintln(w, "None.")
	}
}
