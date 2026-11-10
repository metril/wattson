package store_test

import (
	"strings"
	"testing"
)

func TestRouteAttemptsUsesIndex(t *testing.T) {
	t.Parallel()
	f := newRideFix(t)
	rows, err := f.s.Read.QueryContext(t.Context(),
		`EXPLAIN QUERY PLAN SELECT r.id, r.started_at, r.elapsed_s FROM rides r
		 WHERE r.rider_id=? AND r.route_id=? AND r.ended_at IS NOT NULL ORDER BY r.started_at, r.id`, f.a1.ID, f.route.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail + "\n")
	}
	if !strings.Contains(plan.String(), "rides_rider_route_started") {
		t.Fatalf("index not used:\n%s", plan.String())
	}
}

func TestRouteAttemptsEmpty(t *testing.T) {
	t.Parallel()
	f := newRideFix(t)
	as, err := f.s.RouteAttempts(t.Context(), f.route.ID, f.a1.ID, 0)
	if err != nil || len(as) != 0 {
		t.Fatalf("attempts = %v, %v", as, err)
	}
	d, e, err := f.s.RoutePRTrack(t.Context(), f.route.ID, 0, f.a.ID)
	if err != nil || len(d) != 0 || len(e) != 0 {
		t.Fatalf("track = %v %v %v", d, e, err)
	}
}
