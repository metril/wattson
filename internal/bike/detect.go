package bike

import "github.com/metril/wattson/internal/blue"

type Protocol int

const (
	ProtoUnknown Protocol = iota
	ProtoFitPlus
	ProtoFTMS
	ProtoSim // simulator; never produced by Detect
	ProtoHA  // Home Assistant number entity; never produced by Detect
)

func (p Protocol) String() string {
	switch p {
	case ProtoFitPlus:
		return "fitplus"
	case ProtoFTMS:
		return "ftms"
	case ProtoSim:
		return "sim"
	case ProtoHA:
		return "ha"
	}
	return "unknown"
}

func hasChars(s blue.Service, want ...blue.UUID) bool {
	for _, w := range want {
		found := false
		for _, c := range s.Chars {
			if c.UUID == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func hasService(svcs []blue.Service, u blue.UUID, chars ...blue.UUID) bool {
	for _, s := range svcs {
		if s.UUID == u && hasChars(s, chars...) {
			return true
		}
	}
	return false
}

// Detect prefers FitPlus (fff0 with fff1+fff2), then FTMS (1826 with 2ad9).
func Detect(svcs []blue.Service) Protocol {
	switch {
	case hasService(svcs, FitPlusService, FitPlusNotify, FitPlusWrite):
		return ProtoFitPlus
	case hasService(svcs, FTMSService, FTMSControlPoint):
		return ProtoFTMS
	}
	return ProtoUnknown
}
