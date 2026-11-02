package api

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRidersDefaultCreated(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	for i := range 2 {
		resp, b := ts.do(t, admin, "GET", "/api/riders", nil)
		if resp.StatusCode != 200 {
			t.Fatalf("list: %d %s", resp.StatusCode, b)
		}
		rs := decode[[]RiderOut](t, b)
		if len(rs) != 1 {
			t.Fatalf("call %d: %d riders, want 1: %s", i, len(rs), b)
		}
		if r := rs[0]; r.Name != "admin" || r.WeightKg != 75 || r.MaxLevel != 16 || r.OffsetLevel != 5 || r.MinLevel != 1 ||
			r.BikeKg != 8 || r.FTPW != 200 || r.CdA != 0.32 || r.PowerMultiplier != 1 {
			t.Errorf("default rider = %+v", r)
		}
	}
}

func TestRiderCRUD(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	resp, b := ts.do(t, admin, "POST", "/api/riders", map[string]any{"name": "Guest", "weight_kg": 60})
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, b)
	}
	r := decode[RiderOut](t, b)
	if r.ID == 0 || r.Name != "Guest" || r.WeightKg != 60 || r.BikeKg != 8 || r.MaxLevel != 16 || r.Crr != 0.004 {
		t.Fatalf("created = %+v", r)
	}
	path := fmt.Sprintf("/api/riders/%d", r.ID)
	resp, b = ts.do(t, admin, "PATCH", path, map[string]any{"ftp_w": 250})
	if resp.StatusCode != 200 {
		t.Fatalf("patch: %d %s", resp.StatusCode, b)
	}
	if p := decode[RiderOut](t, b); p.FTPW != 250 || p.WeightKg != 60 || p.Name != "Guest" {
		t.Errorf("patched = %+v", p)
	}
	resp, b = ts.do(t, admin, "GET", path, nil)
	if resp.StatusCode != 200 || decode[RiderOut](t, b).FTPW != 250 {
		t.Errorf("get: %d %s", resp.StatusCode, b)
	}
	// A second rider now exists besides the lazily created default? No: the
	// default is only created by GET /api/riders, so delete needs a sibling.
	if resp, b = ts.do(t, admin, "POST", "/api/riders", map[string]any{"name": "Other"}); resp.StatusCode != 201 {
		t.Fatalf("create other: %d %s", resp.StatusCode, b)
	}
	if resp, b = ts.do(t, admin, "DELETE", path, nil); resp.StatusCode != 204 {
		t.Fatalf("delete: %d %s", resp.StatusCode, b)
	}
	if resp, _ = ts.do(t, admin, "GET", path, nil); resp.StatusCode != 404 {
		t.Errorf("get deleted: %d", resp.StatusCode)
	}
}

func TestRiderValidation(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	resp, b := ts.do(t, admin, "POST", "/api/riders", map[string]any{"name": "A", "weight_kg": 20})
	if resp.StatusCode != 400 || !strings.Contains(string(b), "weight_kg") {
		t.Errorf("weight 20: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "POST", "/api/riders", map[string]any{"name": "A", "min_level": 10, "max_level": 5})
	if resp.StatusCode != 400 || !strings.Contains(string(b), "min_level") {
		t.Errorf("min>max: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "POST", "/api/riders", map[string]any{"name": "   "})
	if resp.StatusCode != 400 || !strings.Contains(string(b), "name") {
		t.Errorf("blank name: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "GET", "/api/riders", nil)
	id := decode[[]RiderOut](t, b)[0].ID
	path := fmt.Sprintf("/api/riders/%d", id)
	// Merged result is validated on PATCH too: default max is 16.
	resp, b = ts.do(t, admin, "PATCH", path, map[string]any{"min_level": 20})
	if resp.StatusCode != 400 || !strings.Contains(string(b), "min_level") {
		t.Errorf("patch min 20: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "PATCH", path, map[string]any{"offset_level": 20})
	if resp.StatusCode != 400 || !strings.Contains(string(b), "offset_level") {
		t.Errorf("patch offset 20: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "PATCH", path, map[string]any{"min_level": 8})
	if resp.StatusCode != 400 || !strings.Contains(string(b), "offset_level") {
		t.Errorf("patch min 8 above offset 5: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "POST", "/api/riders", map[string]any{"name": "A", "offset_level": 30})
	if resp.StatusCode != 400 || !strings.Contains(string(b), "offset_level") {
		t.Errorf("create offset 30: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "PATCH", path, map[string]any{"cda": 5})
	if resp.StatusCode != 400 || !strings.Contains(string(b), "cda") {
		t.Errorf("patch cda: %d %s", resp.StatusCode, b)
	}
}

func TestRiderIsolation(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")
	_, b := ts.do(t, admin, "GET", "/api/riders", nil)
	path := fmt.Sprintf("/api/riders/%d", decode[[]RiderOut](t, b)[0].ID)
	for _, tc := range []struct {
		method string
		body   any
	}{{"GET", nil}, {"PATCH", map[string]any{"ftp_w": 300}}, {"DELETE", nil}} {
		resp, b := ts.do(t, bob, tc.method, path, tc.body)
		if resp.StatusCode != 404 || errCode(t, b) != "not_found" {
			t.Errorf("bob %s: %d %s, want 404", tc.method, resp.StatusCode, b)
		}
	}
	// Bob's own list is his own default, not the admin's.
	_, b = ts.do(t, bob, "GET", "/api/riders", nil)
	if rs := decode[[]RiderOut](t, b); len(rs) != 1 || rs[0].Name != "bob" {
		t.Errorf("bob list = %s", b)
	}
	// The admin rider is untouched.
	if _, b = ts.do(t, admin, "GET", path, nil); decode[RiderOut](t, b).FTPW != 200 {
		t.Errorf("admin rider changed: %s", b)
	}
}

func TestAdminListsAllRiders(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")
	ts.do(t, admin, "GET", "/api/riders", nil)
	ts.do(t, bob, "GET", "/api/riders", nil)
	_, b := ts.do(t, admin, "GET", "/api/riders", nil)
	if rs := decode[[]RiderOut](t, b); len(rs) != 1 {
		t.Errorf("default list = %s", b)
	}
	resp, b := ts.do(t, admin, "GET", "/api/riders?all=true", nil)
	if rs := decode[[]RiderOut](t, b); resp.StatusCode != 200 || len(rs) != 2 || rs[0].UserID == rs[1].UserID {
		t.Errorf("all = %d %s", resp.StatusCode, b)
	}
	if resp, b = ts.do(t, bob, "GET", "/api/riders?all=true", nil); resp.StatusCode != 403 {
		t.Errorf("bob all: %d %s", resp.StatusCode, b)
	}
}

func TestDeleteLastRider(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	_, b := ts.do(t, admin, "GET", "/api/riders", nil)
	path := fmt.Sprintf("/api/riders/%d", decode[[]RiderOut](t, b)[0].ID)
	resp, b := ts.do(t, admin, "DELETE", path, nil)
	if resp.StatusCode != 409 || errCode(t, b) != "last_rider" {
		t.Errorf("delete last: %d %s", resp.StatusCode, b)
	}
}

func TestReadTokenCannotEditRider(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	tok := ts.mintToken(t, "ro", false, "read")
	resp, b := ts.do(t, nil, "GET", "/api/riders", nil, bearer(tok)...)
	if resp.StatusCode != 200 {
		t.Fatalf("read list: %d %s", resp.StatusCode, b)
	}
	path := fmt.Sprintf("/api/riders/%d", decode[[]RiderOut](t, b)[0].ID)
	for _, tc := range []struct {
		method, path string
		body         any
	}{{"PATCH", path, map[string]any{"ftp_w": 300}}, {"POST", "/api/riders", map[string]any{"name": "x"}}, {"DELETE", path, nil}} {
		resp, b := ts.do(t, nil, tc.method, tc.path, tc.body, bearer(tok)...)
		if resp.StatusCode != 403 {
			t.Errorf("%s %s: %d %s, want 403", tc.method, tc.path, resp.StatusCode, b)
		}
	}
}

func TestRiderDeleteInUse(t *testing.T) {
	t.Parallel()
	var busy atomic.Int64
	ts := newTS(t, withRiderInUse(func(id int64) bool { return id == busy.Load() }))
	admin := ts.setupAdmin(t)
	resp, b := ts.do(t, admin, "POST", "/api/riders", map[string]any{"name": "A"})
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, b)
	}
	a := decode[RiderOut](t, b)
	if resp, b = ts.do(t, admin, "POST", "/api/riders", map[string]any{"name": "B"}); resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, b)
	}
	path := fmt.Sprintf("/api/riders/%d", a.ID)
	busy.Store(a.ID)
	resp, b = ts.do(t, admin, "DELETE", path, nil)
	if resp.StatusCode != 409 || !strings.Contains(string(b), `"rider_in_use"`) {
		t.Fatalf("delete in-use: %d %s", resp.StatusCode, b)
	}
	if resp, _ = ts.do(t, admin, "GET", path, nil); resp.StatusCode != 200 {
		t.Errorf("rider gone after refused delete: %d", resp.StatusCode)
	}
	// Someone else's in-use rider is indistinguishable from a missing one.
	ts.addUser(t, "bob", "bob-password-1", false)
	bob := ts.loginAs(t, "bob", "bob-password-1")
	resp, b = ts.do(t, bob, "DELETE", path, nil)
	if resp.StatusCode != 404 || errCode(t, b) != "not_found" {
		t.Errorf("bob delete in-use: %d %s, want 404 not_found", resp.StatusCode, b)
	}
	busy.Store(0)
	if resp, b = ts.do(t, admin, "DELETE", path, nil); resp.StatusCode != 204 {
		t.Errorf("delete after release: %d %s", resp.StatusCode, b)
	}
}

func TestRiderLeadSField(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	resp, b := ts.do(t, admin, "GET", "/api/riders", nil)
	rs := decode[[]RiderOut](t, b)
	if resp.StatusCode != 200 || len(rs) != 1 || rs[0].LeadS != 4 {
		t.Fatalf("list: %d %s", resp.StatusCode, b)
	}
	path := fmt.Sprintf("/api/riders/%d", rs[0].ID)
	resp, b = ts.do(t, admin, "PATCH", path, map[string]any{"lead_s": 20})
	if resp.StatusCode != 400 || !strings.Contains(string(b), "lead_s") {
		t.Errorf("lead_s 20: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "PATCH", path, map[string]any{"lead_s": 7.5})
	if resp.StatusCode != 200 || decode[RiderOut](t, b).LeadS != 7.5 {
		t.Errorf("lead_s 7.5: %d %s", resp.StatusCode, b)
	}
}
