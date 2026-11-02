package api

import (
	"fmt"
	"strings"
	"testing"
)

func ergBody() map[string]any {
	return map[string]any{"name": "Sweet spot", "type": "erg", "blocks": []map[string]any{
		{"duration_s": 60, "ftp_pct": 50},
		{"duration_s": 120, "power_w": 250, "cadence": "90-100"},
	}}
}

func TestWorkoutCreateAndGet(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	resp, b := ts.do(t, admin, "POST", "/api/workouts", ergBody())
	if resp.StatusCode != 201 {
		t.Fatal(resp.StatusCode, string(b))
	}
	w := decode[WorkoutOut](t, b)
	if w.ID == 0 || w.DurationS != 180 || w.Name != "Sweet spot" || w.Type != "erg" || w.OwnerID == 0 || w.CreatedAt.IsZero() {
		t.Fatalf("created = %+v", w)
	}
	resp, b = ts.do(t, admin, "GET", fmt.Sprintf("/api/workouts/%d", w.ID), nil)
	got := decode[WorkoutOut](t, b)
	if resp.StatusCode != 200 || len(got.Blocks) != 2 || got.Blocks[0].FTPPct == nil || *got.Blocks[0].FTPPct != 50 ||
		got.Blocks[0].PowerW != nil || got.Blocks[1].PowerW == nil || *got.Blocks[1].PowerW != 250 || got.Blocks[1].Cadence != "90-100" {
		t.Fatalf("get = %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "GET", "/api/workouts", nil)
	if l := decode[[]WorkoutOut](t, b); resp.StatusCode != 200 || len(l) != 1 || l[0].ID != w.ID || len(l[0].Blocks) != 2 {
		t.Fatalf("list = %d %s", resp.StatusCode, b)
	}

	// replace
	body := ergBody()
	body["name"] = "Renamed"
	body["blocks"] = []map[string]any{{"duration_s": 30, "power_w": 100}}
	resp, b = ts.do(t, admin, "PUT", fmt.Sprintf("/api/workouts/%d", w.ID), body)
	up := decode[WorkoutOut](t, b)
	if resp.StatusCode != 200 || up.Name != "Renamed" || up.DurationS != 30 || len(up.Blocks) != 1 || up.OwnerID != w.OwnerID {
		t.Fatalf("put = %d %s", resp.StatusCode, b)
	}
	resp, _ = ts.do(t, admin, "DELETE", fmt.Sprintf("/api/workouts/%d", w.ID), nil)
	if resp.StatusCode != 204 {
		t.Errorf("delete = %d", resp.StatusCode)
	}
	if resp, _ = ts.do(t, admin, "GET", fmt.Sprintf("/api/workouts/%d", w.ID), nil); resp.StatusCode != 404 {
		t.Errorf("get deleted = %d", resp.StatusCode)
	}
}

func TestWorkoutValidation(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	resp, b := ts.do(t, admin, "POST", "/api/workouts", map[string]any{"name": "x", "type": "interval",
		"blocks": []map[string]any{{"duration_s": 60, "level": 5, "power_w": 100}}})
	if resp.StatusCode != 400 || errCode(t, b) != "invalid_request" || !strings.Contains(string(b), "blocks[0].power_w") {
		t.Errorf("power_w on interval: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "POST", "/api/workouts", map[string]any{"name": "x", "type": "interval",
		"blocks": []map[string]any{{"duration_s": 3, "level": 5}}})
	if resp.StatusCode != 400 {
		t.Errorf("duration 3: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "POST", "/api/workouts", map[string]any{"name": "x", "type": "erg",
		"blocks": []map[string]any{{"duration_s": 60}}})
	if resp.StatusCode != 400 || !strings.Contains(string(b), "blocks[0].power_w") {
		t.Errorf("erg neither: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "POST", "/api/workouts", map[string]any{"name": "x", "type": "interval",
		"blocks": []map[string]any{}})
	if resp.StatusCode != 400 {
		t.Errorf("no blocks: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, admin, "POST", "/api/workouts", map[string]any{"name": "   ", "type": "erg",
		"blocks": []map[string]any{{"duration_s": 60, "power_w": 100}}})
	if resp.StatusCode != 400 || errCode(t, b) != "invalid_request" {
		t.Errorf("blank name: %d %s", resp.StatusCode, b)
	}
	// total over 6 h passes per-field limits but fails Validate
	var many []map[string]any
	for range 4 {
		many = append(many, map[string]any{"duration_s": 7200, "level": 5})
	}
	resp, b = ts.do(t, admin, "POST", "/api/workouts", map[string]any{"name": "x", "type": "interval", "blocks": many})
	if resp.StatusCode != 400 || errCode(t, b) != "invalid_request" || !strings.Contains(string(b), "blocks:") {
		t.Errorf("total: %d %s", resp.StatusCode, b)
	}
	// invalid PUT leaves the stored workout alone
	resp, b = ts.do(t, admin, "POST", "/api/workouts", ergBody())
	w := decode[WorkoutOut](t, b)
	resp, _ = ts.do(t, admin, "PUT", fmt.Sprintf("/api/workouts/%d", w.ID), map[string]any{"name": "x", "type": "interval",
		"blocks": []map[string]any{{"duration_s": 60, "power_w": 100}}})
	if resp.StatusCode != 400 {
		t.Errorf("invalid put: %d", resp.StatusCode)
	}
}

func TestWorkoutsOwnership(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	ts.addUser(t, "bob", testPW, false)
	bob := ts.loginAs(t, "bob", testPW)
	resp, b := ts.do(t, bob, "POST", "/api/workouts", ergBody())
	if resp.StatusCode != 201 {
		t.Fatal(resp.StatusCode, string(b))
	}
	bw := decode[WorkoutOut](t, b)
	resp, b = ts.do(t, admin, "POST", "/api/workouts", ergBody())
	aw := decode[WorkoutOut](t, b)
	ap := fmt.Sprintf("/api/workouts/%d", aw.ID)
	bp := fmt.Sprintf("/api/workouts/%d", bw.ID)

	if resp, _ = ts.do(t, bob, "GET", ap, nil); resp.StatusCode != 200 {
		t.Errorf("bob GET: %d", resp.StatusCode)
	}
	resp, b = ts.do(t, bob, "PUT", ap, ergBody())
	if resp.StatusCode != 403 || errCode(t, b) != "not_owner" {
		t.Errorf("bob PUT: %d %s", resp.StatusCode, b)
	}
	resp, b = ts.do(t, bob, "DELETE", ap, nil)
	if resp.StatusCode != 403 || errCode(t, b) != "not_owner" {
		t.Errorf("bob DELETE: %d %s", resp.StatusCode, b)
	}
	body := ergBody()
	body["name"] = "Admin edit"
	resp, b = ts.do(t, admin, "PUT", bp, body)
	if w := decode[WorkoutOut](t, b); resp.StatusCode != 200 || w.Name != "Admin edit" || w.OwnerID != bw.OwnerID {
		t.Errorf("admin PUT: %d %s", resp.StatusCode, b)
	}
	if resp, _ = ts.do(t, bob, "PUT", bp, ergBody()); resp.StatusCode != 200 {
		t.Errorf("owner PUT: %d", resp.StatusCode)
	}
	if resp, _ = ts.do(t, admin, "DELETE", bp, nil); resp.StatusCode != 204 {
		t.Errorf("admin DELETE: %d", resp.StatusCode)
	}
	if resp, _ = ts.do(t, bob, "DELETE", ap, nil); resp.StatusCode != 403 {
		t.Errorf("bob DELETE admin's: %d", resp.StatusCode)
	}
}

func TestWorkoutNotFound(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	admin := ts.setupAdmin(t)
	for _, m := range []string{"GET", "PUT", "DELETE"} {
		var body any
		if m == "PUT" {
			body = ergBody()
		}
		resp, b := ts.do(t, admin, m, "/api/workouts/999", body)
		if resp.StatusCode != 404 || errCode(t, b) != "not_found" {
			t.Errorf("%s: %d %s", m, resp.StatusCode, b)
		}
	}
}

func TestReadTokenCannotCreateWorkout(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	tok := ts.mintToken(t, "reader", false, "read")
	resp, b := ts.do(t, nil, "POST", "/api/workouts", ergBody(), bearer(tok)...)
	if resp.StatusCode != 403 {
		t.Errorf("read token create: %d %s", resp.StatusCode, b)
	}
	if resp, _ = ts.do(t, nil, "GET", "/api/workouts", nil, bearer(tok)...); resp.StatusCode != 200 {
		t.Errorf("read token list: %d", resp.StatusCode)
	}
	if resp, _ = ts.do(t, nil, "GET", "/api/workouts", nil); resp.StatusCode != 401 {
		t.Errorf("anonymous list: %d", resp.StatusCode)
	}
}

func TestDeleteWorkoutInUseHook(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withWorkoutInUse(func(id int64) bool { return true }))
	admin := ts.setupAdmin(t)
	_, b := ts.do(t, admin, "POST", "/api/workouts", ergBody())
	w := decode[WorkoutOut](t, b)
	resp, b := ts.do(t, admin, "DELETE", fmt.Sprintf("/api/workouts/%d", w.ID), nil)
	if resp.StatusCode != 409 || errCode(t, b) != "workout_in_use" {
		t.Errorf("delete in use: %d %s", resp.StatusCode, b)
	}
}
