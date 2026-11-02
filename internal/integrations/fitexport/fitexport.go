// Package fitexport writes a recorded ride as a FIT Activity file.
package fitexport

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"time"

	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/profile/untyped/mesgnum"
	"github.com/muktihari/fit/proto"

	"github.com/metril/wattson/internal/store"
)

// ErrNoSamples is returned by Encode for a ride without samples.
var ErrNoSamples = errors.New("fitexport: ride has no samples")

const (
	productName = "Wattson"
	// pauseGap is the longest silence between samples that is not a pause.
	pauseGap = 2 * time.Second
)

// Filename is the download name of the ride's FIT file.
func Filename(r store.Ride) string {
	return fmt.Sprintf("wattson-%d-%s.fit", r.ID, r.StartedAt.UTC().Format("20060102-1504"))
}

// Encode writes the ride as a FIT Activity: one record per sample, timer
// events around pauses (gaps over 2 s), and a single lap and session carrying
// the ride summary. Missing position, altitude and heart rate are written as
// FIT invalid values.
func Encode(w io.Writer, r store.Ride, ss []store.Sample) error {
	if len(ss) == 0 {
		return ErrNoSamples
	}
	first, last := ss[0].At.UTC().Truncate(time.Second), ss[len(ss)-1].At.UTC().Truncate(time.Second)
	sum := r.Summary

	act := filedef.NewActivity()
	act.FileId = *mesgdef.NewFileId(nil).
		SetType(typedef.FileActivity).
		SetManufacturer(typedef.ManufacturerDevelopment).
		SetProduct(1).
		SetProductName(productName).
		SetSerialNumber(uint32(r.ID)).
		SetTimeCreated(r.StartedAt.UTC().Truncate(time.Second))
	act.DeviceInfos = append(act.DeviceInfos, mesgdef.NewDeviceInfo(nil).
		SetTimestamp(first).
		SetDeviceIndex(typedef.DeviceIndexCreator).
		SetManufacturer(typedef.ManufacturerDevelopment).
		SetProductName(productName))

	timer := func(at time.Time, et typedef.EventType) {
		act.Events = append(act.Events, mesgdef.NewEvent(nil).
			SetTimestamp(at).SetEvent(typedef.EventTimer).SetEventType(et))
	}
	timer(first, typedef.EventTypeStart)
	for i, s := range ss {
		if i > 0 && s.At.Sub(ss[i-1].At) > pauseGap {
			timer(ss[i-1].At.UTC().Truncate(time.Second), typedef.EventTypeStopAll)
			timer(s.At.UTC().Truncate(time.Second), typedef.EventTypeStart)
		}
		act.Records = append(act.Records, record(s))
	}
	timer(last, typedef.EventTypeStopAll)

	sport, sub := typedef.SportCycling, typedef.SubSportIndoorCycling
	if r.RouteID != 0 || r.Mode == "route" {
		sub = typedef.SubSportVirtualActivity
	}
	// The first sample already carries ElapsedS of riding time, so the
	// session starts that long before it: total_elapsed_time then never falls
	// below total_timer_time.
	start := first.Add(-time.Duration(ss[0].ElapsedS * float64(time.Second))).Truncate(time.Second)
	elapsed := last.Sub(start).Seconds()

	act.Laps = append(act.Laps, mesgdef.NewLap(nil).
		SetTimestamp(last).SetStartTime(start).
		SetEvent(typedef.EventLap).SetEventType(typedef.EventTypeStop).
		SetSport(sport).SetSubSport(sub).
		SetTotalElapsedTimeScaled(elapsed).SetTotalTimerTimeScaled(sum.DurationS).
		SetTotalDistanceScaled(sum.DistanceM).SetTotalAscent(u16(sum.AscentM)).
		SetAvgPower(u16(sum.AvgPowerW)).SetMaxPower(u16(sum.MaxPowerW)).SetNormalizedPower(u16(sum.NPW)).
		SetTotalWork(u32(sum.KJ*1000)).SetAvgCadence(u8(sum.AvgCadenceRpm)).
		SetEnhancedAvgSpeedScaled(sum.AvgSpeedKmh/3.6).SetEnhancedMaxSpeedScaled(sum.MaxSpeedKmh/3.6))
	ses := mesgdef.NewSession(nil).
		SetTimestamp(last).SetStartTime(start).
		SetEvent(typedef.EventSession).SetEventType(typedef.EventTypeStop).
		SetSport(sport).SetSubSport(sub).
		SetTotalElapsedTimeScaled(elapsed).SetTotalTimerTimeScaled(sum.DurationS).
		SetTotalDistanceScaled(sum.DistanceM).SetTotalAscent(u16(sum.AscentM)).
		SetAvgPower(u16(sum.AvgPowerW)).SetMaxPower(u16(sum.MaxPowerW)).SetNormalizedPower(u16(sum.NPW)).
		SetTotalWork(u32(sum.KJ * 1000)).SetAvgCadence(u8(sum.AvgCadenceRpm)).
		SetEnhancedAvgSpeedScaled(sum.AvgSpeedKmh / 3.6).SetEnhancedMaxSpeedScaled(sum.MaxSpeedKmh / 3.6).
		SetFirstLapIndex(0).SetNumLaps(1)
	if sum.AvgHRBpm > 0 {
		ses.SetAvgHeartRate(u8(float64(sum.AvgHRBpm)))
		act.Laps[0].SetAvgHeartRate(u8(float64(sum.AvgHRBpm)))
	}
	if sum.MaxHRBpm > 0 {
		ses.SetMaxHeartRate(u8(float64(sum.MaxHRBpm)))
		act.Laps[0].SetMaxHeartRate(u8(float64(sum.MaxHRBpm)))
	}
	act.Sessions = append(act.Sessions, ses)

	_, off := last.In(time.Local).Zone()
	act.Activity = mesgdef.NewActivity(nil).
		SetTimestamp(last).SetLocalTimestamp(time.Unix(last.Unix()+int64(off), 0).UTC()).
		SetTotalTimerTimeScaled(sum.DurationS).
		SetType(typedef.ActivityManual).SetEvent(typedef.EventActivity).SetEventType(typedef.EventTypeStop).
		SetNumSessions(1)

	fit := act.ToFIT(nil)
	fit.Messages = arrange(fit.Messages)
	if err := encoder.New(w).Encode(&fit); err != nil {
		return fmt.Errorf("fitexport: encode: %w", err)
	}
	return nil
}

// arrange orders the messages as a reader expects: by timestamp, a timer
// start event before the records of its second and a stop after them, then the
// lap, session and activity (which share the final timestamp) last.
func arrange(msgs []proto.Message) []proto.Message {
	var body []proto.Message
	var tail [3][]proto.Message // lap, session, activity
	for _, m := range msgs {
		switch m.Num {
		case mesgnum.Lap:
			tail[0] = append(tail[0], m)
		case mesgnum.Session:
			tail[1] = append(tail[1], m)
		case mesgnum.Activity:
			tail[2] = append(tail[2], m)
		default:
			body = append(body, m)
		}
	}
	rank := func(m proto.Message) int {
		if m.Num != mesgnum.Event {
			return 1
		}
		if mesgdef.NewEvent(&m).EventType == typedef.EventTypeStart {
			return 0
		}
		return 2
	}
	stamp := func(m proto.Message) (uint32, bool) {
		f := m.FieldByNum(proto.FieldNumTimestamp)
		if f == nil {
			return 0, false
		}
		return f.Value.Uint32(), true
	}
	slices.SortStableFunc(body, func(a, b proto.Message) int {
		ta, oka := stamp(a)
		tb, okb := stamp(b)
		switch {
		case !oka && !okb:
			return 0
		case !oka:
			return -1
		case !okb:
			return 1
		}
		return cmp.Or(cmp.Compare(ta, tb), cmp.Compare(rank(a), rank(b)))
	})
	for _, t := range tail {
		body = append(body, t...)
	}
	return body
}

func record(s store.Sample) *mesgdef.Record {
	rec := mesgdef.NewRecord(nil).
		SetTimestamp(s.At.UTC().Truncate(time.Second)).
		SetDistanceScaled(s.DistanceM).
		SetEnhancedSpeedScaled(s.SpeedKmh / 3.6).
		SetPower(u16(s.PowerW)).
		SetCadence(u8(s.CadenceRpm))
	if !math.IsNaN(s.Lat) && !math.IsNaN(s.Lon) {
		rec.SetPositionLatDegrees(s.Lat).SetPositionLongDegrees(s.Lon)
	}
	if !math.IsNaN(s.EleM) {
		rec.SetEnhancedAltitudeScaled(s.EleM)
	}
	if s.HRBpm > 0 {
		rec.SetHeartRate(u8(float64(s.HRBpm)))
	}
	return rec
}

// u8, u16 and u32 round v into the unsigned range below the FIT invalid value.
func u8(v float64) uint8   { return uint8(clamp(v, math.MaxUint8-1)) }
func u16(v float64) uint16 { return uint16(clamp(v, math.MaxUint16-1)) }
func u32(v float64) uint32 { return uint32(clamp(v, math.MaxUint32-1)) }

func clamp(v, hi float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Min(math.Max(math.Round(v), 0), hi)
}
