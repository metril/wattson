package mapillary

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/metril/wattson/internal/route"
)

const (
	lat0 = 45.5
	lon0 = 10.0
)

var mPerDegLon = 111320.0 * math.Cos(lat0*math.Pi/180)

// eastProfile is a due-east route of length metres from (lat0, lon0), 10 m samples.
func eastProfile(length float64) *route.Profile {
	n := int(length/10) + 1
	p := &route.Profile{StepM: 10, LengthM: length}
	for i := 0; i < n; i++ {
		p.Lat = append(p.Lat, lat0)
		p.Lon = append(p.Lon, lon0+float64(i)*10/mPerDegLon)
		p.Ele = append(p.Ele, 0)
		p.Grade = append(p.Grade, 0)
	}
	return p
}

// at returns a candidate dAlong metres east and north metres north of the route start.
func at(id string, dAlong, north, compass float64) Candidate {
	return Candidate{ID: id, Lat: lat0 + north/111320, Lon: lon0 + dAlong/mPerDegLon, Compass: compass}
}

func TestSelectKeepsNearForward(t *testing.T) {
	t.Parallel()
	got := Select(eastProfile(1000), []Candidate{at("a", 300, 5, 90)}, SelectOpts{})
	if len(got) != 1 {
		t.Fatalf("got %d photos", len(got))
	}
	if math.Abs(got[0].DM-300) > 1 || got[0].ID != "a" || got[0].BearingDeg != 90 {
		t.Errorf("photo = %+v", got[0])
	}
}

func TestSelectRejectsFarAndBackward(t *testing.T) {
	t.Parallel()
	p := eastProfile(1000)
	if got := Select(p, []Candidate{at("far", 300, 30, 90)}, SelectOpts{}); len(got) != 0 {
		t.Errorf("30 m lateral kept: %+v", got)
	}
	if got := Select(p, []Candidate{at("back", 300, 2, 270)}, SelectOpts{}); len(got) != 0 {
		t.Errorf("backward kept: %+v", got)
	}
	pano := at("pano", 300, 2, 270)
	pano.Pano = true
	if got := Select(p, []Candidate{pano}, SelectOpts{}); len(got) != 1 || !got[0].Pano {
		t.Errorf("pano = %+v", got)
	}
	// 59 degrees off is kept, 61 is not.
	if got := Select(p, []Candidate{at("a", 300, 2, 90+59)}, SelectOpts{}); len(got) != 1 {
		t.Errorf("59 deg off dropped")
	}
	if got := Select(p, []Candidate{at("a", 300, 2, 90+61)}, SelectOpts{}); len(got) != 0 {
		t.Errorf("61 deg off kept")
	}
	// Wraps the short way: compass 350 vs a due-east (90) segment is 100 off; 30 vs 90 is 60 off.
	if got := Select(p, []Candidate{at("a", 300, 2, 350)}, SelectOpts{}); len(got) != 0 {
		t.Errorf("compass 350 kept")
	}
	// Beyond the ends of the route is rejected by lateral distance.
	if got := Select(p, []Candidate{at("a", -100, 0, 90), at("b", 1100, 0, 90)}, SelectOpts{}); len(got) != 0 {
		t.Errorf("off-route ends kept: %+v", got)
	}
}

func TestSelectBucketBest(t *testing.T) {
	t.Parallel()
	a := at("a", 303, 8, 90)
	a.CapturedAt = 3000
	b := at("b", 305, 2, 90)
	b.CapturedAt = 1000
	c := at("c", 307, 2, 90)
	c.CapturedAt = 2000
	got := Select(eastProfile(1000), []Candidate{a, b, c}, SelectOpts{})
	if len(got) != 1 || got[0].ID != "c" {
		t.Fatalf("got %+v, want only c", got)
	}
	// Equal score and date: smaller id wins, whatever the input order.
	d, e := at("9", 303, 2, 90), at("1", 303, 2, 90)
	got = Select(eastProfile(1000), []Candidate{d, e}, SelectOpts{})
	if len(got) != 1 || got[0].ID != "1" {
		t.Fatalf("tie got %+v, want id 1", got)
	}
	// Candidates in different buckets are both kept, sorted by distance.
	got = Select(eastProfile(1000), []Candidate{at("far", 500, 0, 90), at("near", 100, 0, 90)}, SelectOpts{})
	if len(got) != 2 || got[0].ID != "near" || got[1].ID != "far" {
		t.Errorf("two buckets = %+v", got)
	}
}

func TestSelectHairpin(t *testing.T) {
	t.Parallel()
	// 1 km out due east, then straight back on the same line (2 km total).
	out := eastProfile(1000)
	p := &route.Profile{StepM: 10, LengthM: 2000}
	for i := 0; i <= 200; i++ {
		j := i
		if i > 100 {
			j = 200 - i
		}
		p.Lat = append(p.Lat, out.Lat[j])
		p.Lon = append(p.Lon, out.Lon[j])
		p.Ele = append(p.Ele, 0)
		p.Grade = append(p.Grade, 0)
	}
	west := Select(p, []Candidate{at("w", 400, 2, 270)}, SelectOpts{})
	if len(west) != 1 || west[0].DM < 1000 || math.Abs(west[0].DM-1600) > 2 {
		t.Fatalf("west = %+v, want DM near 1600", west)
	}
	east := Select(p, []Candidate{at("e", 400, 2, 90)}, SelectOpts{})
	if len(east) != 1 || math.Abs(east[0].DM-400) > 2 {
		t.Fatalf("east = %+v, want DM near 400", east)
	}
}

func TestSelectDegenerate(t *testing.T) {
	t.Parallel()
	cands := []Candidate{at("a", 0, 0, 90)}
	if got := Select(nil, cands, SelectOpts{}); got != nil {
		t.Errorf("nil profile = %+v", got)
	}
	if got := Select(&route.Profile{StepM: 10}, cands, SelectOpts{}); got != nil {
		t.Errorf("empty profile = %+v", got)
	}
	// Duplicate points make zero-length segments: no NaN, no panic.
	p := eastProfile(100)
	p.Lat[3], p.Lon[3] = p.Lat[2], p.Lon[2]
	got := Select(p, []Candidate{at("a", 20, 1, 90), at("b", 25, 1, 90)}, SelectOpts{})
	for _, ph := range got {
		if math.IsNaN(ph.DM) {
			t.Errorf("NaN DM: %+v", ph)
		}
	}
	if len(got) == 0 {
		t.Error("nothing selected near duplicate points")
	}
}

func TestSelectMaxThins(t *testing.T) {
	t.Parallel()
	const n = 20000
	p := eastProfile(float64(n) * 15)
	cands := make([]Candidate, n)
	for i := range cands {
		cands[i] = at(fmt.Sprint(i+1), float64(i)*15+7, 1, 90)
	}
	rand.Shuffle(len(cands), func(i, j int) { cands[i], cands[j] = cands[j], cands[i] })
	got := Select(p, cands, SelectOpts{Max: 8000})
	if len(got) == 0 || len(got) > 8000 {
		t.Fatalf("got %d photos", len(got))
	}
	if !slices.IsSortedFunc(got, func(a, b Photo) int {
		if a.DM < b.DM {
			return -1
		} else if a.DM > b.DM {
			return 1
		}
		return 0
	}) {
		t.Error("not sorted by DM")
	}
	if got[0].ID != "1" || got[len(got)-1].ID != fmt.Sprint(n) {
		t.Errorf("first %s last %s, want 1 and %d", got[0].ID, got[len(got)-1].ID, n)
	}
	// Under the cap nothing is dropped.
	if all := Select(p, cands, SelectOpts{Max: n}); len(all) != n {
		t.Errorf("uncapped = %d, want %d", len(all), n)
	}
}

func TestTiles(t *testing.T) {
	t.Parallel()
	p := eastProfile(1000)
	x0, y0 := tileXY(lat0, lon0, 14)
	x1, y1 := tileXY(p.Lat[len(p.Lat)-1], p.Lon[len(p.Lon)-1], 14)
	var want [][3]int
	for x := min(x0, x1); x <= max(x0, x1); x++ {
		for y := min(y0, y1); y <= max(y0, y1); y++ {
			want = append(want, [3]int{14, x, y})
		}
	}
	if got := Tiles(p, 14, 0); !slices.Equal(got, want) {
		t.Errorf("0 m buffer = %v, want %v", got, want)
	}
	big := Tiles(p, 14, 2000)
	if len(big) <= len(want) {
		t.Errorf("2 km buffer gave %d tiles, not more than %d", len(big), len(want))
	}
	seen := map[[3]int]bool{}
	for _, tl := range big {
		if seen[tl] {
			t.Errorf("duplicate tile %v", tl)
		}
		seen[tl] = true
	}
	for _, tl := range want {
		if !seen[tl] {
			t.Errorf("buffered result misses %v", tl)
		}
	}
	if !slices.IsSortedFunc(big, func(a, b [3]int) int {
		for i := range a {
			if a[i] != b[i] {
				return a[i] - b[i]
			}
		}
		return 0
	}) {
		t.Error("tiles not sorted")
	}
}

func TestTilesSparseProfileHasNoGaps(t *testing.T) {
	t.Parallel()
	// Two samples 20 km apart: every tile between them is touched.
	p := &route.Profile{StepM: 20000, LengthM: 20000,
		Lat: []float64{lat0, lat0}, Lon: []float64{lon0, lon0 + 20000/mPerDegLon}}
	got := Tiles(p, 14, 0)
	x0, _ := tileXY(lat0, lon0, 14)
	x1, _ := tileXY(lat0, p.Lon[1], 14)
	if len(got) != x1-x0+1 {
		t.Errorf("got %d tiles, want %d", len(got), x1-x0+1)
	}
}

func benchSetup() (*route.Profile, []Candidate) {
	p := eastProfile(100_000)
	rng := rand.New(rand.NewPCG(1, 2))
	cands := make([]Candidate, 20000)
	for i := range cands {
		along := rng.Float64() * 100_000
		cands[i] = at(fmt.Sprint(i), along, (rng.Float64()-0.5)*50, 90+(rng.Float64()-0.5)*100)
	}
	return p, cands
}

func BenchmarkSelect100km(b *testing.B) {
	p, cands := benchSetup()
	b.ResetTimer()
	for b.Loop() {
		Select(p, cands, SelectOpts{})
	}
}

// TestSelectBudget gates the 100 km selection at 200 ms/op. It is not
// parallel: timing beside other busy tests would be flaky.
func TestSelectBudget(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing budget is only enforced in non-race, non-short runs")
	}
	r := testing.Benchmark(BenchmarkSelect100km)
	if r.N == 0 {
		t.Fatal("benchmark did not run")
	}
	t.Logf("100 km select: %.1f ms/op", float64(r.NsPerOp())/1e6)
	if ns := r.NsPerOp(); ns >= 200e6 {
		t.Fatalf("select took %.1f ms/op, budget 200 ms", float64(ns)/1e6)
	}
}

func TestSelectPanoUnknownHeadingJSONSafe(t *testing.T) {
	t.Parallel()
	pano := at("p", 300, 2, math.NaN())
	pano.Pano = true
	flat := at("n", 500, 2, math.NaN()) // non-pano without a heading cannot be judged: dropped
	got := Select(eastProfile(1000), []Candidate{pano, flat}, SelectOpts{})
	if len(got) != 1 || got[0].ID != "p" || got[0].BearingDeg != 0 {
		t.Fatalf("got %+v", got)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Errorf("marshal: %v", err)
	}
}

func TestSelectWideLateralScansFarCells(t *testing.T) {
	t.Parallel()
	// 400 m north is two 0.002 degree cell rows away from the route.
	got := Select(eastProfile(1000), []Candidate{at("a", 300, 400, 90)}, SelectOpts{MaxLateralM: 500})
	if len(got) != 1 || math.Abs(got[0].DM-300) > 1 {
		t.Fatalf("got %+v", got)
	}
}
