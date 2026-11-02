package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

func TestWorkoutCRUD(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := storetest.New(t)
	u, _ := s.CreateUser(ctx, store.User{Username: "a"})
	w, err := s.CreateWorkout(ctx, store.Workout{UserID: u.ID, Name: "Sweet spot", Type: "erg",
		Blocks: []byte(`[{"duration_s":60,"power_w":200}]`), DurationS: 60})
	if err != nil || w.ID <= 0 || w.CreatedAt.IsZero() || w.UpdatedAt.IsZero() {
		t.Fatalf("create = %+v, %v", w, err)
	}
	got, err := s.Workout(ctx, w.ID)
	if err != nil || got.Name != "Sweet spot" || got.Type != "erg" || got.UserID != u.ID || got.DurationS != 60 ||
		string(got.Blocks) != `[{"duration_s":60,"power_w":200}]` || !got.CreatedAt.Equal(w.CreatedAt) {
		t.Fatalf("get = %+v, %v", got, err)
	}
	time.Sleep(2 * time.Millisecond)
	w2, err := s.CreateWorkout(ctx, store.Workout{UserID: u.ID, Name: "Later", Type: "interval",
		Blocks: []byte(`[{"duration_s":30,"level":5}]`), DurationS: 30})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	up, err := s.UpdateWorkout(ctx, store.Workout{ID: w.ID, Name: "Renamed", Type: "interval",
		Blocks: []byte(`[{"duration_s":90,"level":7}]`), DurationS: 90})
	if err != nil || up.Name != "Renamed" || up.Type != "interval" || up.DurationS != 90 ||
		string(up.Blocks) != `[{"duration_s":90,"level":7}]` || !up.UpdatedAt.After(w.UpdatedAt) ||
		!up.CreatedAt.Equal(w.CreatedAt) || up.UserID != u.ID {
		t.Fatalf("update = %+v, %v", up, err)
	}
	if _, err := s.UpdateWorkout(ctx, store.Workout{ID: 999, Name: "x", Type: "erg", Blocks: []byte(`[]`)}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update missing: %v", err)
	}
	list, err := s.ListWorkouts(ctx)
	if err != nil || len(list) != 2 || list[0].ID != w2.ID || list[1].ID != w.ID || string(list[0].Blocks) == "" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := s.DeleteWorkout(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Workout(ctx, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get deleted: %v", err)
	}
	if err := s.DeleteWorkout(ctx, w.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}

func TestWorkoutRejectsBadType(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	if _, err := s.CreateWorkout(t.Context(), store.Workout{Name: "x", Type: "other", Blocks: []byte(`[]`)}); err == nil {
		t.Fatal("expected CHECK failure")
	}
}

func TestWorkoutUserDeleteSetsNull(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := storetest.New(t)
	u, _ := s.CreateUser(ctx, store.User{Username: "a"})
	w, _ := s.CreateWorkout(ctx, store.Workout{UserID: u.ID, Name: "w", Type: "erg", Blocks: []byte(`[]`)})
	if _, err := s.Write.ExecContext(ctx, `DELETE FROM users WHERE id=?`, u.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Workout(ctx, w.ID)
	if err != nil || got.UserID != 0 {
		t.Fatalf("after owner delete = %+v, %v", got, err)
	}
}

func TestPowerModelStore(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := storetest.New(t)
	if _, err := s.PowerModel(ctx, "bike-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("absent: %v", err)
	}
	if err := s.PutPowerModel(ctx, "bike-1", []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutPowerModel(ctx, "bike-1", []byte(`{"a":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutPowerModel(ctx, "bike-2", []byte(`{"a":3}`)); err != nil {
		t.Fatal(err)
	}
	b, err := s.PowerModel(ctx, "bike-1")
	if err != nil || string(b) != `{"a":2}` {
		t.Fatalf("get = %s, %v", b, err)
	}
	if err := s.DeletePowerModel(ctx, "bike-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PowerModel(ctx, "bike-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
	if err := s.DeletePowerModel(ctx, "bike-1"); err != nil {
		t.Errorf("delete again: %v", err)
	}
	if _, err := s.PowerModel(ctx, "bike-2"); err != nil {
		t.Errorf("other key gone: %v", err)
	}
}
