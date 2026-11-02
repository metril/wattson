package route

import (
	"errors"
	"slices"
	"testing"
)

func TestTileName(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		lat, lon float64
		want     string
	}{
		{45.2, 7.9, "E5_N45"}, {-33.9, 18.4, "E15_S35"}, {51.5, -0.1, "W5_N50"},
		{64.1, -21.9, "W25_N60"}, {0, 0, "E0_N0"}, {-0.1, -179.9, "W180_S5"},
		{90, 180, "E175_N85"},
	} {
		if got := TileName(c.lat, c.lon); got != c.want {
			t.Errorf("TileName(%v,%v)=%q want %q", c.lat, c.lon, got, c.want)
		}
	}
}

func TestParseTile(t *testing.T) {
	t.Parallel()
	lat, lon, err := ParseTile("E5_N45")
	if err != nil || lat != 45 || lon != 5 {
		t.Fatalf("got %d %d %v", lat, lon, err)
	}
	for _, n := range []string{"W180_S5", "E175_N85", "E0_N0"} {
		if _, _, err := ParseTile(n); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
	for _, n := range []string{"../x", "E7_N45", "E180_N45", "E5_N90", "e5_n45", "E05_N45", "W0_N0", "E5_S0", "", "E5_N45/"} {
		if _, _, err := ParseTile(n); !errors.Is(err, ErrBadTileName) {
			t.Errorf("%q: got %v", n, err)
		}
	}
}

func TestTilesForLine(t *testing.T) {
	t.Parallel()
	got := TilesForLine([]LonLat{{9.9, 45.5}, {10.1, 45.5}})
	if want := []string{"E10_N45", "E5_N45"}; !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// A long leg crossing many tiles with no waypoint inside them.
	got = TilesForLine([]LonLat{{0.5, 45.5}, {20.5, 45.5}})
	if len(got) != 5 {
		t.Fatalf("got %v", got)
	}
}
