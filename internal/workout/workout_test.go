package workout_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/workout"
)

func ip(v int) *int { return &v }

func blocks(n int, mk func(i int) workout.Block) []workout.Block {
	bs := make([]workout.Block, n)
	for i := range bs {
		bs[i] = mk(i)
	}
	return bs
}

func ivl(d, lvl int) workout.Block { return workout.Block{DurationS: d, Level: ip(lvl)} }
func erg(d, w int) workout.Block   { return workout.Block{DurationS: d, PowerW: ip(w)} }

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		typ   workout.Type
		bs    []workout.Block
		field string // "" means valid
	}{
		{"valid interval", workout.Interval, []workout.Block{ivl(60, 5), {DurationS: 30, LevelOffset: ip(-3), Label: "easy", Cadence: "80-90"}}, ""},
		{"valid erg", workout.Erg, []workout.Block{erg(60, 150), {DurationS: 60, FTPPct: ip(75)}}, ""},
		{"bad type", workout.Type("x"), []workout.Block{ivl(60, 5)}, "type"},
		{"empty", workout.Interval, nil, "blocks"},
		{"201 blocks", workout.Interval, blocks(201, func(int) workout.Block { return ivl(10, 5) }), "blocks"},
		{"short block", workout.Interval, []workout.Block{ivl(60, 5), ivl(60, 5), ivl(4, 5)}, "blocks[2].duration_s"},
		{"long block", workout.Interval, []workout.Block{ivl(7201, 5)}, "blocks[0].duration_s"},
		{"total too long", workout.Interval, blocks(4, func(i int) workout.Block {
			if i == 3 {
				return ivl(3601, 5)
			}
			return ivl(6000, 5)
		}), "blocks"},
		{"both level and offset", workout.Interval, []workout.Block{{DurationS: 60, Level: ip(5), LevelOffset: ip(1)}}, "blocks[0].level"},
		{"neither level nor offset", workout.Interval, []workout.Block{{DurationS: 60}}, "blocks[0].level"},
		{"level 0", workout.Interval, []workout.Block{ivl(60, 0)}, "blocks[0].level"},
		{"level 33", workout.Interval, []workout.Block{ivl(60, 33)}, "blocks[0].level"},
		{"offset 32", workout.Interval, []workout.Block{{DurationS: 60, LevelOffset: ip(32)}}, "blocks[0].level_offset"},
		{"interval with power_w", workout.Interval, []workout.Block{{DurationS: 60, Level: ip(5), PowerW: ip(100)}}, "blocks[0].power_w"},
		{"interval with ftp_pct", workout.Interval, []workout.Block{{DurationS: 60, Level: ip(5), FTPPct: ip(50)}}, "blocks[0].ftp_pct"},
		{"erg with level", workout.Erg, []workout.Block{{DurationS: 60, PowerW: ip(100), Level: ip(5)}}, "blocks[0].level"},
		{"erg with offset", workout.Erg, []workout.Block{{DurationS: 60, PowerW: ip(100), LevelOffset: ip(5)}}, "blocks[0].level_offset"},
		{"erg neither", workout.Erg, []workout.Block{erg(60, 100), {DurationS: 60}}, "blocks[1].power_w"},
		{"erg both", workout.Erg, []workout.Block{{DurationS: 60, PowerW: ip(100), FTPPct: ip(50)}}, "blocks[0].power_w"},
		{"erg power 49", workout.Erg, []workout.Block{erg(60, 49)}, "blocks[0].power_w"},
		{"erg power 1001", workout.Erg, []workout.Block{erg(60, 1001)}, "blocks[0].power_w"},
		{"erg ftp_pct 250", workout.Erg, []workout.Block{{DurationS: 60, FTPPct: ip(250)}}, "blocks[0].ftp_pct"},
		{"erg ftp_pct 19", workout.Erg, []workout.Block{{DurationS: 60, FTPPct: ip(19)}}, "blocks[0].ftp_pct"},
		{"cadence 33 chars", workout.Interval, []workout.Block{{DurationS: 60, Level: ip(5), Cadence: strings.Repeat("a", 33)}}, "blocks[0].cadence"},
		{"label 41 chars", workout.Interval, []workout.Block{{DurationS: 60, Level: ip(5), Label: strings.Repeat("é", 41)}}, "blocks[0].label"},
		{"label 40 runes ok", workout.Interval, []workout.Block{{DurationS: 60, Level: ip(5), Label: strings.Repeat("é", 40)}}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := workout.Validate(tc.typ, tc.bs)
			if tc.field == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			var ve *workout.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want *ValidationError", err)
			}
			if ve.Field != tc.field {
				t.Errorf("field = %q (%v), want %q", ve.Field, err, tc.field)
			}
			if !strings.HasPrefix(err.Error(), tc.field+": ") {
				t.Errorf("Error() = %q", err.Error())
			}
		})
	}
}

func TestValidateMaxTotalExact(t *testing.T) {
	t.Parallel()
	bs := blocks(3, func(int) workout.Block { return ivl(7200, 5) })
	if err := workout.Validate(workout.Interval, bs); err != nil {
		t.Fatalf("21600 s total: %v", err)
	}
	if got := workout.DurationS(bs); got != 21600 {
		t.Errorf("DurationS = %d", got)
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	r := store.Rider{OffsetLevel: 5, MinLevel: 2, MaxLevel: 16, FTPW: 200}
	got := workout.Resolve(workout.Interval, []workout.Block{
		{DurationS: 60, LevelOffset: ip(-10)}, {DurationS: 90, LevelOffset: ip(3)}, ivl(120, 20), ivl(30, 1),
	}, r)
	want := []engine.WorkoutBlock{
		{Duration: 60 * time.Second, Level: 2}, {Duration: 90 * time.Second, Level: 8},
		{Duration: 120 * time.Second, Level: 16}, {Duration: 30 * time.Second, Level: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("block %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	// never level 0, even for a rider whose min level is 0
	z := workout.Resolve(workout.Interval, []workout.Block{{DurationS: 10, LevelOffset: ip(-31)}},
		store.Rider{OffsetLevel: 5, MinLevel: 0, MaxLevel: 16})
	if z[0].Level != 1 {
		t.Errorf("floor = %d, want 1", z[0].Level)
	}
	eg := workout.Resolve(workout.Erg, []workout.Block{{DurationS: 60, FTPPct: ip(75)}, erg(45, 230)}, r)
	if eg[0].TargetW != 150 || eg[0].Duration != time.Minute || eg[1].TargetW != 230 || eg[1].Duration != 45*time.Second {
		t.Errorf("erg = %+v", eg)
	}
}
