package bike

import (
	"testing"

	"github.com/metril/wattson/internal/blue"
)

func svc(u blue.UUID, chars ...blue.UUID) blue.Service {
	s := blue.Service{UUID: u}
	for _, c := range chars {
		s.Chars = append(s.Chars, blue.Characteristic{UUID: c})
	}
	return s
}

func TestDetect(t *testing.T) {
	t.Parallel()
	fit := svc(FitPlusService, FitPlusNotify, FitPlusWrite)
	ftms := svc(FTMSService, FTMSControlPoint, FTMSIndoorBikeData)
	cases := []struct {
		name string
		in   []blue.Service
		want Protocol
	}{
		{"fitplus", []blue.Service{fit}, ProtoFitPlus},
		{"ftms", []blue.Service{ftms}, ProtoFTMS},
		{"fitplus wins over ftms", []blue.Service{ftms, fit}, ProtoFitPlus},
		{"unknown", []blue.Service{svc(blue.UUID16(0x180f))}, ProtoUnknown},
		{"none", nil, ProtoUnknown},
		{"fitplus missing write", []blue.Service{svc(FitPlusService, FitPlusNotify)}, ProtoUnknown},
		{"ftms missing control point", []blue.Service{svc(FTMSService, FTMSIndoorBikeData)}, ProtoUnknown},
	}
	for _, c := range cases {
		if got := Detect(c.in); got != c.want {
			t.Errorf("%s: %v want %v", c.name, got, c.want)
		}
	}
}

func TestProtocolString(t *testing.T) {
	t.Parallel()
	if ProtoUnknown.String() != "unknown" || ProtoFitPlus.String() != "fitplus" || ProtoFTMS.String() != "ftms" || Protocol(9).String() != "unknown" {
		t.Fatal("strings")
	}
}
