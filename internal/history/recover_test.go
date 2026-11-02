package history_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/history"
	"github.com/metril/wattson/internal/ridectl"
	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

func seedRider(t *testing.T, s *store.Store) store.Rider {
	t.Helper()
	u, err := s.CreateUser(t.Context(), store.User{Username: "u"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateRider(t.Context(), store.DefaultRider(u.ID, "R"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func openRide(t *testing.T, s *store.Store, rider int64, n int) store.Ride {
	t.Helper()
	r, err := s.CreateRide(t.Context(), store.Ride{RiderID: rider, Mode: "free", StartedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	ss := make([]store.Sample, n)
	for i := range ss {
		ss[i] = store.Sample{Seq: i, At: t0.Add(time.Duration(i) * time.Second), ElapsedS: float64(i + 1),
			DistanceM: float64(10 * (i + 1)), PowerW: 200, Lat: math.NaN(), Lon: math.NaN(), EleM: math.NaN()}
	}
	if err := s.AppendSamples(t.Context(), r.ID, ss); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRecover(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := storetest.New(t)
	rider := seedRider(t, s)
	long := openRide(t, s, rider.ID, 12)
	short := openRide(t, s, rider.ID, 3)
	var got []store.Ride
	fin, del, err := history.Recover(ctx, s, 10, func(_ context.Context, r store.Ride) { got = append(got, r) })
	if err != nil || fin != 1 || del != 1 {
		t.Fatalf("Recover = %d, %d, %v", fin, del, err)
	}
	rides, err := s.ListRides(ctx, store.RideQuery{})
	if err != nil || len(rides) != 1 || rides[0].ID != long.ID || rides[0].SampleCount != 12 ||
		rides[0].Summary.DurationS != 12 || rides[0].EndedAt == nil || !rides[0].EndedAt.Equal(t0.Add(11*time.Second)) {
		t.Fatalf("rides = %+v, %v", rides, err)
	}
	if len(got) != 1 || got[0].ID != long.ID || got[0].EndedAt == nil {
		t.Errorf("onFinished = %+v", got)
	}
	if _, err := s.Ride(ctx, short.ID); err == nil {
		t.Error("short ride should be deleted")
	}
	if open, _ := s.OpenRides(ctx); len(open) != 0 {
		t.Errorf("open = %d", len(open))
	}
}

func TestRecorderWithStore(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	rider := seedRider(t, s)
	ev := make(chan ridectl.Event, 64)
	rec := history.NewRecorder(history.RecorderDeps{Store: s, FlushEvery: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- rec.Run(ctx, ev) }()
	ev <- ridectl.Event{Type: "status", Status: &ridectl.Status{RiderID: rider.ID, State: "ready"}}
	for i := range 12 {
		tk := riding(i)
		ev <- ridectl.Event{Type: "tick", Tick: &tk}
	}
	fin := riding(11)
	fin.State = engine.StateFinished
	ev <- ridectl.Event{Type: "tick", Tick: &fin}
	deadline := time.Now().Add(2 * time.Second)
	var rides []store.Ride
	for time.Now().Before(deadline) {
		rides, _ = s.ListRides(t.Context(), store.RideQuery{})
		if len(rides) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if len(rides) != 1 || rides[0].SampleCount != 12 || rides[0].RiderID != rider.ID {
		t.Fatalf("rides = %+v", rides)
	}
}
