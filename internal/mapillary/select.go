package mapillary

import (
	"cmp"
	"math"
	"slices"

	"github.com/metril/wattson/internal/route"
)

// Photo is one street photo placed on the route.
type Photo struct {
	ID         string  `json:"id" doc:"Mapillary image id"`
	DM         float64 `json:"d_m" doc:"Distance along the route, metres"`
	Lat        float64 `json:"lat" doc:"Latitude, degrees"`
	Lon        float64 `json:"lon" doc:"Longitude, degrees"`
	BearingDeg float64 `json:"bearing_deg" doc:"Camera heading, degrees clockwise from north"`
	CapturedAt int64   `json:"captured_at" doc:"Capture time, Unix milliseconds"`
	Pano       bool    `json:"pano" doc:"360° panorama"`
}

// SelectOpts tunes Select; a zero field takes its default.
type SelectOpts struct {
	MaxLateralM   float64 // default 20: farthest an image may be from the route
	MaxBearingDeg float64 // default 60: widest angle between camera and travel direction (non-pano)
	BucketM       float64 // default 15: one photo is kept per this much route
	Max           int     // default 8000: most photos returned
}

func (o SelectOpts) withDefaults() SelectOpts {
	if !(o.MaxLateralM > 0) {
		o.MaxLateralM = 20
	}
	if !(o.MaxBearingDeg > 0) {
		o.MaxBearingDeg = 60
	}
	if !(o.BucketM > 0) {
		o.BucketM = 15
	}
	if o.Max <= 0 {
		o.Max = 8000
	}
	return o
}

const (
	cellDeg    = 0.002 // spatial hash cell, about 220 m of latitude
	metersPerD = 111320.0
)

type segment struct {
	aLat, aLon, bLat, bLon float64
	d0, d1                 float64 // route distance at each end
	bearing                float64 // degrees clockwise from north
}

type cellKey [2]int32

func cellOf(lat, lon float64) cellKey {
	return cellKey{int32(math.Floor(lat / cellDeg)), int32(math.Floor(lon / cellDeg))}
}

// bearingDiff is the shortest arc between two compass angles, 0 to 180.
func bearingDiff(a, b float64) float64 {
	d := math.Mod(math.Abs(a-b), 360)
	if d > 180 {
		d = 360 - d
	}
	return d
}

// Select projects candidates onto the route and keeps the best photo per
// BucketM of route, sorted by distance along it.
//
// A candidate belongs to the nearest route segment that it faces (a
// non-panorama must point within MaxBearingDeg of the segment's direction of
// travel), so on an out-and-back it joins the leg it was shot on. Within a
// bucket the lowest score = lateral metres + bearing degrees off / 6 wins,
// then the newest capture, then the smaller id. Over Max photos, every other
// one is dropped until it fits; the first and last are always kept.
func Select(p *route.Profile, cands []Candidate, o SelectOpts) []Photo {
	if p == nil || p.Len() < 2 || len(p.Lon) != p.Len() {
		return nil
	}
	o = o.withDefaults()

	// Spatial hash of segments, each in every cell its bounding box covers.
	grid := make(map[cellKey][]int32)
	var segs []segment
	for i := 0; i+1 < p.Len(); i++ {
		s := segment{aLat: p.Lat[i], aLon: p.Lon[i], bLat: p.Lat[i+1], bLon: p.Lon[i+1], d0: p.DistAt(i), d1: p.DistAt(i + 1)}
		// Segments crossing the antimeridian are not supported: skipped.
		if !(s.d1 > s.d0) || !finite(s.aLat, s.aLon, s.bLat, s.bLon) || math.Abs(s.bLon-s.aLon) > 180 {
			continue
		}
		dx := (s.bLon - s.aLon) * math.Cos((s.aLat+s.bLat)/2*math.Pi/180) * metersPerD
		dy := (s.bLat - s.aLat) * metersPerD
		if math.Hypot(dx, dy) < 0.01 {
			continue // duplicate point: no direction
		}
		s.bearing = math.Mod(math.Atan2(dx, dy)*180/math.Pi+360, 360)
		idx := int32(len(segs))
		segs = append(segs, s)
		lo, hi := cellOf(min(s.aLat, s.bLat), min(s.aLon, s.bLon)), cellOf(max(s.aLat, s.bLat), max(s.aLon, s.bLon))
		for la := lo[0]; la <= hi[0]; la++ {
			for lo2 := lo[1]; lo2 <= hi[1]; lo2++ {
				k := cellKey{la, lo2}
				grid[k] = append(grid[k], idx)
			}
		}
	}
	if len(segs) == 0 {
		return nil
	}

	type scored struct {
		Photo
		score float64
	}
	buckets := make(map[int]scored)
	for _, c := range cands {
		if !finite(c.Lat, c.Lon) || (!c.Pano && !finite(c.Compass)) {
			continue // a non-panorama needs a heading to be judged
		}
		compass := c.Compass
		if !finite(compass) {
			compass = 0 // pano with an unknown heading; keeps the photo list JSON-safe
		}
		cosLat := math.Cos(c.Lat * math.Pi / 180)
		ck := cellOf(c.Lat, c.Lon)
		// Cells are narrower in metres east-west than north-south; scan enough
		// of them to cover MaxLateralM in both directions.
		r := int32(min(50, max(1, math.Ceil(o.MaxLateralM/(cellDeg*metersPerD*max(0.01, cosLat))))))

		var best *segment
		var bestLat, bestAlong, bestDiff float64
		for da := -r; da <= r; da++ {
			for db := -r; db <= r; db++ {
				for _, si := range grid[cellKey{ck[0] + da, ck[1] + db}] {
					s := &segs[si]
					diff := 0.0
					if !c.Pano {
						if diff = bearingDiff(c.Compass, s.bearing); diff > o.MaxBearingDeg {
							continue
						}
					}
					// Local frame with the candidate at the origin, metres east and north.
					ax, ay := (s.aLon-c.Lon)*cosLat*metersPerD, (s.aLat-c.Lat)*metersPerD
					bx, by := (s.bLon-c.Lon)*cosLat*metersPerD, (s.bLat-c.Lat)*metersPerD
					ex, ey := bx-ax, by-ay
					t := -(ax*ex + ay*ey) / (ex*ex + ey*ey)
					t = max(0, min(1, t))
					lateral := math.Hypot(ax+t*ex, ay+t*ey)
					if lateral > o.MaxLateralM {
						continue
					}
					along := s.d0 + t*(s.d1-s.d0)
					if best == nil || lateral < bestLat || (lateral == bestLat && (diff < bestDiff || (diff == bestDiff && along < bestAlong))) {
						best, bestLat, bestAlong, bestDiff = s, lateral, along, diff
					}
				}
			}
		}
		if best == nil {
			continue
		}
		ph := scored{
			Photo: Photo{ID: c.ID, DM: bestAlong, Lat: c.Lat, Lon: c.Lon, BearingDeg: compass, CapturedAt: c.CapturedAt, Pano: c.Pano},
			score: bestLat + bestDiff/6,
		}
		b := int(math.Floor(bestAlong / o.BucketM))
		if cur, ok := buckets[b]; !ok || cmp.Or(
			cmp.Compare(ph.score, cur.score),
			-cmp.Compare(ph.CapturedAt, cur.CapturedAt), // newer first
			cmp.Compare(ph.ID, cur.ID),
		) < 0 {
			buckets[b] = ph
		}
	}

	out := make([]Photo, 0, len(buckets))
	for _, ph := range buckets {
		out = append(out, ph.Photo)
	}
	slices.SortFunc(out, func(a, b Photo) int { return cmp.Or(cmp.Compare(a.DM, b.DM), cmp.Compare(a.ID, b.ID)) })
	for len(out) > o.Max {
		if len(out) <= 2 {
			out = out[:o.Max]
			break
		}
		kept := out[:0]
		for i, ph := range out {
			if i%2 == 0 || i == len(out)-1 {
				kept = append(kept, ph)
			}
		}
		out = kept
	}
	return out
}

func finite(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

// tileXY is the Web Mercator slippy-map tile containing lat, lon at zoom z.
func tileXY(lat, lon float64, z int) (x, y int) {
	n := math.Exp2(float64(z))
	lat = max(-85.0511, min(85.0511, lat))
	fx := (lon + 180) / 360 * n
	fy := (1 - math.Asinh(math.Tan(lat*math.Pi/180))/math.Pi) / 2 * n
	hi := int(n) - 1
	return max(0, min(hi, int(math.Floor(fx)))), max(0, min(hi, int(math.Floor(fy))))
}

// Tiles lists the z/x/y tiles touched by the route buffered by bufM metres
// (a square buffer, so a slight superset), deduplicated and sorted.
func Tiles(p *route.Profile, z int, bufM float64) [][3]int {
	if p == nil || p.Len() == 0 || len(p.Lon) != p.Len() {
		return nil
	}
	bufM = max(0, bufM)
	maxGap := 360 / math.Exp2(float64(z)) / 2 // sub-steps of half a tile so none is skipped
	seen := make(map[[3]int]struct{})
	add := func(lat, lon float64) {
		if !finite(lat, lon) {
			return
		}
		dLat := bufM / metersPerD
		dLon := bufM / (metersPerD * max(0.01, math.Cos(lat*math.Pi/180)))
		x0, y1 := tileXY(lat-dLat, lon-dLon, z) // larger y is further south
		x1, y0 := tileXY(lat+dLat, lon+dLon, z)
		for x := x0; x <= x1; x++ {
			for y := y0; y <= y1; y++ {
				seen[[3]int{z, x, y}] = struct{}{}
			}
		}
	}
	for i := 0; i < p.Len(); i++ {
		add(p.Lat[i], p.Lon[i])
		if i+1 == p.Len() {
			break
		}
		gap := max(math.Abs(p.Lat[i+1]-p.Lat[i]), math.Abs(p.Lon[i+1]-p.Lon[i]))
		if gap > maxGap && gap <= 180 && finite(gap) { // no antimeridian wrapping
			n := int(math.Ceil(gap / maxGap))
			for k := 1; k < n; k++ {
				f := float64(k) / float64(n)
				add(p.Lat[i]+f*(p.Lat[i+1]-p.Lat[i]), p.Lon[i]+f*(p.Lon[i+1]-p.Lon[i]))
			}
		}
	}
	out := make([][3]int, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b [3]int) int {
		return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1]), cmp.Compare(a[2], b[2]))
	})
	return out
}
