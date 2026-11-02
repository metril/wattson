package ridectl

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/workout"
)

var wkRider = store.Rider{ID: 7, UserID: 1, Name: "r", MinLevel: 1, MaxLevel: 16, OffsetLevel: 5, FTPW: 200, PowerMultiplier: 1,
	WeightKg: 75, BikeKg: 8, CdA: 0.32, Crr: 0.004}

func ip(v int) *int { return &v }

// wkStore is a mutable set of workouts behind Deps.LoadWorkout.
type wkStore struct {
	mu sync.Mutex
	ws map[int64]store.Workout
}

func newWk(id int64, typ string, bs ...workout.Block) *wkStore {
	s := &wkStore{ws: map[int64]store.Workout{}}
	s.set(id, typ, bs...)
	return s
}

func (s *wkStore) set(id int64, typ string, bs ...workout.Block) {
	raw, _ := json.Marshal(bs)
	s.mu.Lock()
	s.ws[id] = store.Workout{ID: id, Name: "w", Type: typ, Blocks: raw, DurationS: workout.DurationS(bs)}
	s.mu.Unlock()
}

func (s *wkStore) load(_ context.Context, id int64) (store.Workout, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.ws[id]
	if !ok {
		return store.Workout{}, ErrWorkoutNotFound
	}
	return w, nil
}

func readyWk(t *testing.T, s *wkStore) *Manager {
	t.Helper()
	m := newMgr(&simRec{}, 20*time.Millisecond, nil, Deps{LoadWorkout: s.load})
	if _, err := m.Connect(t.Context(), wkRider); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "ready")
	return m
}

func TestStartWorkoutInterval(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := readyWk(t, newWk(5, "interval", workout.Block{DurationS: 10, Level: ip(4)}, workout.Block{DurationS: 10, Level: ip(9)}))
		defer m.Close(context.Background())
		ch, cancel := m.Subscribe(1 << 16)
		defer cancel()
		if err := m.StartWorkout(t.Context(), 5); err != nil {
			t.Fatal(err)
		}
		if !m.WorkoutInUse(5) || m.WorkoutInUse(6) {
			t.Error("WorkoutInUse wrong while riding")
		}
		var four, nine, finished bool
		for range 30 {
			time.Sleep(time.Second)
			for _, tk := range drainTicks(ch) {
				switch {
				case tk.State == engine.StateFinished:
					finished = true
				case tk.State == engine.StateRiding && tk.Mode == "interval":
					if tk.WorkoutID != 5 || tk.BlockCount != 2 {
						t.Fatalf("tick = %+v", tk)
					}
					four = four || (tk.ElapsedS > 2 && tk.ElapsedS < 9 && tk.Level == 4)
					nine = nine || (tk.ElapsedS >= 10.5 && tk.Level == 9)
				}
			}
		}
		if !four || !nine || !finished {
			t.Errorf("four=%v nine=%v finished=%v", four, nine, finished)
		}
		waitState(t, m, "ready")
		if m.WorkoutInUse(5) {
			t.Error("workout still in use after finishing")
		}
	})
}

func TestStartWorkoutErgFTP(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := readyWk(t, newWk(8, "erg", workout.Block{DurationS: 60, FTPPct: ip(75)}))
		defer m.Close(context.Background())
		if err := m.StartWorkout(t.Context(), 8); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, "riding")
		time.Sleep(30 * time.Second)
		tk := m.Snapshot()
		if tk.Mode != "erg" || tk.WorkoutID != 8 || tk.TargetPowerW != 150 {
			t.Fatalf("tick = %+v", tk)
		}
		if math.Abs(tk.PowerW-150) > 7.5 {
			t.Errorf("power %v not within 5%% of 150", tk.PowerW)
		}
	})
}

func TestStartErgFree(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := readyWk(t, newWk(1, "erg", workout.Block{DurationS: 60, PowerW: ip(100)}))
		defer m.Close(context.Background())
		if err := m.StartErg(t.Context(), 200); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, "riding")
		time.Sleep(2 * time.Second)
		if tk := m.Snapshot(); tk.Mode != "erg" || tk.WorkoutID != 0 || tk.TargetPowerW != 200 {
			t.Fatalf("tick = %+v", tk)
		}
		if err := m.Do(t.Context(), engine.Command{Kind: engine.CmdSetTargetPower, Value: 250}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, 5*time.Second, "target 250", func() bool { return m.Snapshot().TargetPowerW == 250 })
	})
}

func TestStartWorkoutUnknown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := readyWk(t, newWk(5, "interval", workout.Block{DurationS: 10, Level: ip(4)}))
		defer m.Close(context.Background())
		if err := m.StartWorkout(t.Context(), 99); !errors.Is(err, ErrWorkoutNotFound) {
			t.Fatalf("StartWorkout = %v", err)
		}
		if m.Status().State != "ready" {
			t.Errorf("state = %s", m.Status().State)
		}
	})
}

func TestStartWorkoutInvalidStored(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newWk(5, "interval", workout.Block{DurationS: 1, Level: ip(4)}) // below the 5 s minimum
		m := readyWk(t, s)
		defer m.Close(context.Background())
		if err := m.StartWorkout(t.Context(), 5); !errors.Is(err, ErrWorkoutNotFound) {
			t.Fatalf("StartWorkout = %v", err)
		}
	})
}

func TestStartErgRange(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		m := readyWk(t, newWk(5, "interval", workout.Block{DurationS: 10, Level: ip(4)}))
		defer m.Close(context.Background())
		for _, w := range []int{20, 1001} {
			if err := m.StartErg(t.Context(), w); !errors.Is(err, engine.ErrBadValue) {
				t.Errorf("StartErg(%d) = %v", w, err)
			}
		}
		if m.Status().State != "ready" {
			t.Errorf("state = %s", m.Status().State)
		}
	})
}

func TestStartWorkoutIdleAndBusy(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newWk(5, "interval", workout.Block{DurationS: 10, Level: ip(4)})
		idle := newMgr(&simRec{}, 20*time.Millisecond, nil, Deps{LoadWorkout: s.load})
		defer idle.Close(context.Background())
		if err := idle.StartWorkout(t.Context(), 5); !errors.Is(err, ErrNoBike) {
			t.Errorf("idle StartWorkout = %v", err)
		}
		if err := idle.StartErg(t.Context(), 200); !errors.Is(err, ErrNoBike) {
			t.Errorf("idle StartErg = %v", err)
		}
		m := readyWk(t, s)
		defer m.Close(context.Background())
		if err := m.StartWorkout(t.Context(), 5); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, "riding")
		if err := m.StartWorkout(t.Context(), 5); !errors.Is(err, engine.ErrBadState) {
			t.Errorf("second StartWorkout = %v", err)
		}
		if err := m.StartErg(t.Context(), 200); !errors.Is(err, engine.ErrBadState) {
			t.Errorf("StartErg while riding = %v", err)
		}
	})
}

func TestEditDuringRideNoEffect(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newWk(5, "interval", workout.Block{DurationS: 60, Level: ip(4)}, workout.Block{DurationS: 60, Level: ip(9)})
		m := readyWk(t, s)
		defer m.Close(context.Background())
		if err := m.StartWorkout(t.Context(), 5); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, "riding")
		s.set(5, "interval", workout.Block{DurationS: 60, Level: ip(2)})
		time.Sleep(10 * time.Second)
		if tk := m.Snapshot(); tk.BlockCount != 2 || tk.WorkoutID != 5 {
			t.Errorf("tick = %+v, want the original 2 blocks", tk)
		}
	})
}
