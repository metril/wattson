package bike

import "testing"

func TestClampLevelTable(t *testing.T) {
	t.Parallel()
	cases := []struct{ req, min, max, want int }{
		{0, 1, 32, 1}, {-5, 1, 32, 1}, {33, 1, 32, 32}, {16, 1, 32, 16},
		{0, 0, 32, 1}, {5, 10, 32, 10}, {40, 10, 20, 20},
		{7, 1, 0, 1}, {7, 1, -3, 1}, {3, 8, 4, 4}, // max wins over min; never below 1
	}
	for _, c := range cases {
		if got := ClampLevel(c.req, c.min, c.max); got != c.want {
			t.Errorf("ClampLevel(%d,%d,%d)=%d want %d", c.req, c.min, c.max, got, c.want)
		}
	}
}

func FuzzClampLevel(f *testing.F) {
	for _, s := range [][3]int{{0, 1, 32}, {-1, 0, 0}, {100, 5, 20}, {1 << 62, -1 << 62, 1 << 62}, {-1 << 63, -1 << 63, -1 << 63}} {
		f.Add(s[0], s[1], s[2])
	}
	f.Fuzz(func(t *testing.T, r, mn, mx int) {
		got := ClampLevel(r, mn, mx)
		hi := mx
		if hi < 1 {
			hi = 1
		}
		if got < 1 || got > hi {
			t.Fatalf("ClampLevel(%d,%d,%d)=%d outside [1,%d]", r, mn, mx, got, hi)
		}
	})
}

func FuzzSetLevelNeverZero(f *testing.F) {
	for _, s := range [][3]int{{0, 1, 32}, {-1, 0, 0}, {100, 5, 40}, {1 << 62, 1, 1 << 62}, {-1 << 63, -1 << 63, -1 << 63}, {5, 1, 32}} {
		f.Add(s[0], s[1], s[2])
	}
	f.Fuzz(func(t *testing.T, r, mn, mx int) {
		lvl := ClampLevel(r, mn, mx)
		if fb, err := FitPlusSetLevel(lvl); err == nil {
			if len(fb) != 7 || fb[3] == 0 || fb[3] != byte(lvl) {
				t.Fatalf("FitPlus level %d frame % x", lvl, fb)
			}
		}
		for _, enc := range []ResistanceEncoding{EncU8Tenths, EncU16Tenths} {
			if fr, err := FTMSSetResistance(lvl, enc); err == nil {
				if len(fr) < 2 || fr[0] != 0x04 {
					t.Fatalf("FTMS frame % x", fr)
				}
				nz := false
				for _, b := range fr[1:] {
					nz = nz || b != 0
				}
				if !nz {
					t.Fatalf("FTMS level %d enc %d: all-zero param % x", lvl, enc, fr)
				}
			}
		}
	})
}
