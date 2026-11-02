package fitexport_test

import (
	"bytes"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/profile/basetype"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/proto"

	"github.com/metril/wattson/internal/history"
	"github.com/metril/wattson/internal/integrations/fitexport"
	"github.com/metril/wattson/internal/store"
)

var t0 = time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

// ramp120 mirrors the history test ride, with a position every sample.
func ramp120() []store.Sample {
	out := make([]store.Sample, 120)
	for i := range out {
		s := store.Sample{Seq: i, At: t0.Add(time.Duration(i) * time.Second), ElapsedS: float64(i + 1),
			DistanceM: float64(10 * (i + 1)), SpeedKmh: 36, Level: 5,
			Lat: 45 + float64(i)*1e-4, Lon: 7, EleM: float64(100 + i)}
		if i >= 60 {
			s.PowerW, s.CadenceRpm = 400, 90
		}
		switch {
		case i < 10:
		case i < 60:
			s.HRBpm = 120
		default:
			s.HRBpm = 160
		}
		out[i] = s
	}
	return out
}

func decode(t *testing.T, b []byte) *filedef.Activity {
	t.Helper()
	lis := filedef.NewListener()
	defer lis.Close()
	dec := decoder.New(bytes.NewReader(b), decoder.WithMesgListener(lis), decoder.WithBroadcastOnly())
	n := 0
	for dec.Next() {
		if _, err := dec.Decode(); err != nil {
			t.Fatalf("decode: %v", err)
		}
		n++
	}
	if n != 1 {
		t.Fatalf("decoded %d sequences", n)
	}
	act, ok := lis.File().(*filedef.Activity)
	if !ok {
		t.Fatal("not an activity file")
	}
	return act
}

func encode(t *testing.T, r store.Ride, ss []store.Sample) *filedef.Activity {
	t.Helper()
	var buf bytes.Buffer
	if err := fitexport.Encode(&buf, r, ss); err != nil {
		t.Fatal(err)
	}
	return decode(t, buf.Bytes())
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	ss := ramp120()
	r := store.Ride{ID: 7, RouteID: 3, Mode: "route", StartedAt: t0, Summary: history.Summarize(ss)}
	act := encode(t, r, ss)
	if act.FileId.Type != typedef.FileActivity || act.FileId.Manufacturer != typedef.ManufacturerDevelopment ||
		act.FileId.ProductName != "Wattson" || act.FileId.SerialNumber != 7 {
		t.Errorf("file id = %+v", act.FileId)
	}
	if len(act.Records) != 120 {
		t.Fatalf("records = %d", len(act.Records))
	}
	if got := act.Records[0].PositionLatDegrees(); !near(got, 45, 1e-6) {
		t.Errorf("lat[0] = %v", got)
	}
	last := act.Records[119]
	if !near(last.DistanceScaled(), 1200, 0.01) || !near(last.EnhancedSpeedScaled(), 10, 0.001) ||
		last.Power != 400 || last.Cadence != 90 || last.HeartRate != 160 || !near(last.EnhancedAltitudeScaled(), 219, 0.2) {
		t.Errorf("last record = %+v", last)
	}
	for i, rec := range act.Records {
		if !rec.Timestamp.Equal(ss[i].At.Truncate(time.Second)) {
			t.Fatalf("record %d timestamp = %v, want %v", i, rec.Timestamp, ss[i].At)
		}
	}
	if len(act.Sessions) != 1 || len(act.Laps) != 1 {
		t.Fatalf("sessions=%d laps=%d", len(act.Sessions), len(act.Laps))
	}
	for _, s := range []struct {
		name string
		get  func() (sport typedef.Sport, sub typedef.SubSport, dist, timer float64, avg, mx, np uint16, work uint32, asc uint16, hr uint8)
	}{
		{"session", func() (typedef.Sport, typedef.SubSport, float64, float64, uint16, uint16, uint16, uint32, uint16, uint8) {
			x := act.Sessions[0]
			return x.Sport, x.SubSport, x.TotalDistanceScaled(), x.TotalTimerTimeScaled(), x.AvgPower, x.MaxPower, x.NormalizedPower, x.TotalWork, x.TotalAscent, x.AvgHeartRate
		}},
		{"lap", func() (typedef.Sport, typedef.SubSport, float64, float64, uint16, uint16, uint16, uint32, uint16, uint8) {
			x := act.Laps[0]
			return x.Sport, x.SubSport, x.TotalDistanceScaled(), x.TotalTimerTimeScaled(), x.AvgPower, x.MaxPower, x.NormalizedPower, x.TotalWork, x.TotalAscent, x.AvgHeartRate
		}},
	} {
		sport, sub, dist, timer, avg, mx, np, work, asc, hr := s.get()
		if sport != typedef.SportCycling || sub != typedef.SubSportVirtualActivity || !near(dist, 1200, 0.01) ||
			!near(timer, 120, 0.001) || avg != 200 || mx != 400 || np != 318 || work != 24000 || asc != 119 || hr != 142 {
			t.Errorf("%s = %v %v dist %v timer %v avg %d max %d np %d work %d asc %d hr %d",
				s.name, sport, sub, dist, timer, avg, mx, np, work, asc, hr)
		}
	}
	if got := act.Sessions[0].TotalElapsedTimeScaled(); !near(got, 120, 0.001) {
		t.Errorf("total elapsed = %v, want 120 (first sample carries 1 s of timer time)", got)
	}
	if !act.Sessions[0].StartTime.Equal(t0.Add(-time.Second)) {
		t.Errorf("start time = %v", act.Sessions[0].StartTime)
	}
	if act.Activity != nil && !near(act.Activity.TotalTimerTimeScaled(), 120, 0.001) {
		t.Errorf("activity timer = %v", act.Activity.TotalTimerTimeScaled())
	}
	if act.Sessions[0].NumLaps != 1 || act.Sessions[0].MaxHeartRate != 160 {
		t.Errorf("session = laps %d maxhr %d", act.Sessions[0].NumLaps, act.Sessions[0].MaxHeartRate)
	}
	if act.Activity == nil || act.Activity.NumSessions != 1 || act.Activity.Type != typedef.ActivityManual ||
		!act.Activity.Timestamp.Equal(ss[119].At) {
		t.Errorf("activity = %+v", act.Activity)
	}
	if len(act.DeviceInfos) != 1 || act.DeviceInfos[0].ProductName != "Wattson" {
		t.Errorf("device infos = %+v", act.DeviceInfos)
	}
}

func TestFreeRideNoPosition(t *testing.T) {
	t.Parallel()
	ss := ramp120()
	for i := range ss {
		ss[i].Lat, ss[i].Lon, ss[i].EleM, ss[i].HRBpm = math.NaN(), math.NaN(), math.NaN(), 0
	}
	r := store.Ride{ID: 8, Mode: "free", StartedAt: t0, Summary: history.Summarize(ss)}
	act := encode(t, r, ss)
	rec := act.Records[0]
	if rec.PositionLat != basetype.Sint32Invalid || rec.PositionLong != basetype.Sint32Invalid ||
		rec.EnhancedAltitude != basetype.Uint32Invalid || rec.HeartRate != basetype.Uint8Invalid {
		t.Errorf("record = %+v", rec)
	}
	if act.Sessions[0].SubSport != typedef.SubSportIndoorCycling {
		t.Errorf("sub sport = %v", act.Sessions[0].SubSport)
	}
	if act.Sessions[0].AvgHeartRate != basetype.Uint8Invalid || act.Sessions[0].MaxHeartRate != basetype.Uint8Invalid {
		t.Errorf("session hr = %d/%d, want invalid", act.Sessions[0].AvgHeartRate, act.Sessions[0].MaxHeartRate)
	}
}

func TestPauseEvents(t *testing.T) {
	t.Parallel()
	ss := ramp120()
	for i := 60; i < len(ss); i++ {
		ss[i].At = ss[i].At.Add(time.Minute)
	}
	r := store.Ride{ID: 9, Mode: "free", StartedAt: t0, Summary: history.Summarize(ss)}
	act := encode(t, r, ss)
	var types []typedef.EventType
	var at []time.Time
	for _, e := range act.Events {
		if e.Event == typedef.EventTimer {
			types = append(types, e.EventType)
			at = append(at, e.Timestamp)
		}
	}
	want := []typedef.EventType{typedef.EventTypeStart, typedef.EventTypeStopAll, typedef.EventTypeStart, typedef.EventTypeStopAll}
	if len(types) != 4 || types[0] != want[0] || types[1] != want[1] || types[2] != want[2] || types[3] != want[3] {
		t.Fatalf("timer events = %v", types)
	}
	if !at[1].Equal(ss[59].At) || !at[2].Equal(ss[60].At) || !at[3].Equal(ss[119].At) {
		t.Errorf("event times = %v", at)
	}
	s := act.Sessions[0]
	if !near(s.TotalElapsedTimeScaled(), 180, 0.001) || !near(s.TotalTimerTimeScaled(), 120, 0.001) {
		t.Errorf("elapsed %v timer %v", s.TotalElapsedTimeScaled(), s.TotalTimerTimeScaled())
	}
}

func TestPauseThreshold(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		shift time.Duration // added to samples 30.. so the gap there is 1 s + shift
		want  int
	}{{"2s gap is not a pause", time.Second, 2}, {"2.5s gap is a pause", 1500 * time.Millisecond, 4}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ss := ramp120()
			for i := 30; i < len(ss); i++ {
				ss[i].At = ss[i].At.Add(c.shift)
			}
			act := encode(t, store.Ride{ID: 9, Mode: "free", StartedAt: t0, Summary: history.Summarize(ss)}, ss)
			n := 0
			for _, e := range act.Events {
				if e.Event == typedef.EventTimer {
					n++
				}
			}
			if n != c.want {
				t.Errorf("timer events = %d, want %d", n, c.want)
			}
		})
	}
}

func TestSummariesAreLast(t *testing.T) {
	t.Parallel()
	ss := ramp120()
	var buf bytes.Buffer
	if err := fitexport.Encode(&buf, store.Ride{ID: 1, Summary: history.Summarize(ss)}, ss); err != nil {
		t.Fatal(err)
	}
	var nums []uint16
	lis := &numLister{out: &nums}
	dec := decoder.New(bytes.NewReader(buf.Bytes()), decoder.WithMesgListener(lis))
	if _, err := dec.Decode(); err != nil {
		t.Fatal(err)
	}
	n := len(nums)
	// file_id(0) first; lap(19), session(18), activity(34) last
	if nums[0] != 0 || nums[n-3] != 19 || nums[n-2] != 18 || nums[n-1] != 34 {
		t.Errorf("message order = first %d, last three %v", nums[0], nums[n-3:])
	}
	// The timer start precedes the first record and the stop follows the last.
	const event, record = 21, 20
	firstEv, firstRec, lastEv, lastRec := -1, -1, -1, -1
	for i, m := range nums[:n-3] {
		switch m {
		case event:
			if firstEv < 0 {
				firstEv = i
			}
			lastEv = i
		case record:
			if firstRec < 0 {
				firstRec = i
			}
			lastRec = i
		}
	}
	if firstEv < 0 || firstEv > firstRec || lastEv < lastRec {
		t.Errorf("events at %d..%d, records at %d..%d", firstEv, lastEv, firstRec, lastRec)
	}
}

type numLister struct{ out *[]uint16 }

func (l *numLister) OnMesg(m proto.Message) { *l.out = append(*l.out, uint16(m.Num)) }

func TestEncodeEmpty(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := fitexport.Encode(&buf, store.Ride{ID: 1}, nil); !errors.Is(err, fitexport.ErrNoSamples) {
		t.Errorf("err = %v", err)
	}
	if buf.Len() != 0 {
		t.Error("wrote bytes for an empty ride")
	}
}

func TestFilename(t *testing.T) {
	t.Parallel()
	r := store.Ride{ID: 7, StartedAt: time.Date(2026, 11, 2, 7, 5, 0, 0, time.FixedZone("x", 2*3600))}
	if got := fitexport.Filename(r); got != "wattson-7-20261102-0505.fit" {
		t.Errorf("Filename = %q", got)
	}
}
