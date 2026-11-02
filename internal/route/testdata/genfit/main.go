// Command genfit writes the "ramp" FIT Course fixture (11 points due north
// from 45.0000, 7.0000, 0.0009 degrees apart, ele 100..150 m).
//
//	go run ./internal/route/testdata/genfit [out.fit]
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
)

func main() {
	out := "internal/route/testdata/ramp.fit"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := run(out); err != nil {
		fmt.Fprintln(os.Stderr, "genfit:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	c := filedef.NewCourse()
	c.FileId.SetType(typedef.FileCourse).
		SetManufacturer(typedef.ManufacturerDevelopment).
		SetProduct(1).
		SetSerialNumber(1).
		SetTimeCreated(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	c.Course = mesgdef.NewCourse(nil).SetName("Ramp").SetSport(typedef.SportCycling)
	for i := 0; i < 11; i++ {
		c.Records = append(c.Records, mesgdef.NewRecord(nil).
			SetPositionLatDegrees(45+float64(i)*0.0009).
			SetPositionLongDegrees(7).
			SetEnhancedAltitudeScaled(100+5*float64(i)))
	}
	fit := c.ToFIT(nil)
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	return encoder.New(f).Encode(&fit)
}
