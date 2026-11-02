package store_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

func TestRiderStore(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := storetest.New(t)
	a, err := s.CreateUser(ctx, store.User{Username: "a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateUser(ctx, store.User{Username: "b"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateRider(ctx, store.DefaultRider(a.ID, "A"))
	if err != nil || r.ID == 0 || r.CreatedAt.IsZero() {
		t.Fatalf("create = %+v, %v", r, err)
	}
	if _, err := s.Rider(ctx, b.ID, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("other user's Rider: %v", err)
	}
	other := r
	other.UserID, other.FTPW = b.ID, 999
	if _, err := s.UpdateRider(ctx, other); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("other user's Update: %v", err)
	}
	r.FTPW = 250
	if u, err := s.UpdateRider(ctx, r); err != nil || u.FTPW != 250 || u.CreatedAt != r.CreatedAt {
		t.Errorf("update = %+v, %v", u, err)
	}
	if err := s.DeleteRider(ctx, a.ID, r.ID); !errors.Is(err, store.ErrLastRider) {
		t.Errorf("delete last: %v", err)
	}
	if err := s.DeleteRider(ctx, b.ID, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete other's: %v", err)
	}
	r2, _ := s.CreateRider(ctx, store.DefaultRider(a.ID, "A2"))
	if err := s.DeleteRider(ctx, a.ID, r.ID); err != nil {
		t.Errorf("delete with sibling: %v", err)
	}
	if rs, _ := s.ListRiders(ctx, a.ID); len(rs) != 1 || rs[0].ID != r2.ID {
		t.Errorf("list = %+v", rs)
	}
}

func TestEnsureRiderIsAtomic(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := storetest.New(t)
	u, err := s.CreateUser(ctx, store.User{Username: "a"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.EnsureRider(ctx, store.DefaultRider(u.ID, "A")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if rs, _ := s.ListRiders(ctx, u.ID); len(rs) != 1 {
		t.Errorf("%d riders, want 1", len(rs))
	}
}
