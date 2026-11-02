package route

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
)

// ErrBadTileName is returned by ParseTile for anything that is not a canonical
// BRouter 5 degree tile name.
var ErrBadTileName = errors.New("route: bad tile name")

var tileRE = regexp.MustCompile(`^([EW])(\d{1,3})_([NS])(\d{1,2})$`)

// TileName returns the name of the 5 degree BRouter tile holding the point,
// named after its south-west corner (E5_N45, W5_N50, E15_S35). Out-of-range
// input is clamped; NaN yields "".
func TileName(lat, lon float64) string {
	if math.IsNaN(lat) || math.IsNaN(lon) {
		return ""
	}
	lat0 := int(math.Floor(max(-90, min(85, math.Floor(lat/5)*5))))
	lon0 := int(math.Floor(max(-180, min(175, math.Floor(lon/5)*5))))
	return tileString(lat0, lon0)
}

func tileString(lat0, lon0 int) string {
	ew, ns := "E", "N"
	if lon0 < 0 {
		ew, lon0 = "W", -lon0
	}
	if lat0 < 0 {
		ns, lat0 = "S", -lat0
	}
	return fmt.Sprintf("%s%d_%s%d", ew, lon0, ns, lat0)
}

// ParseTile is the inverse of TileName and validates untrusted names.
func ParseTile(name string) (lat0, lon0 int, err error) {
	m := tileRE.FindStringSubmatch(name)
	if m == nil {
		return 0, 0, ErrBadTileName
	}
	lon0, _ = strconv.Atoi(m[2])
	lat0, _ = strconv.Atoi(m[4])
	if m[1] == "W" {
		lon0 = -lon0
	}
	if m[3] == "S" {
		lat0 = -lat0
	}
	if lon0%5 != 0 || lat0%5 != 0 || lon0 < -180 || lon0 > 175 || lat0 < -90 || lat0 > 85 ||
		tileString(lat0, lon0) != name { // rejects leading zeros, W0, S0
		return 0, 0, ErrBadTileName
	}
	return lat0, lon0, nil
}

const (
	tileSampleM  = 1000.0
	maxLegSample = 25000 // bound the work for absurd legs
)

// TilesForLine lists the tiles touched by every waypoint and by points every
// 1 km along each straight leg, sorted and unique.
func TilesForLine(wps []LonLat) []string {
	set := map[string]struct{}{}
	add := func(lat, lon float64) {
		if n := TileName(lat, lon); n != "" {
			set[n] = struct{}{}
		}
	}
	for i, w := range wps {
		add(w[1], w[0])
		if i == 0 {
			continue
		}
		a := wps[i-1]
		n := int(math.Min(maxLegSample, math.Ceil(haversine(a[1], a[0], w[1], w[0])/tileSampleM)))
		for k := 1; k < n; k++ {
			t := float64(k) / float64(n)
			add(a[1]+(w[1]-a[1])*t, lerpLon(a[0], w[0], t))
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
