package route

import (
	"io"
	"math"

	"github.com/tkrajina/gpxgo/gpx"
)

// ParseGPX returns all trk/trkseg points in order, else the rte points.
// name is the first track name, else the metadata name.
func ParseGPX(r io.Reader) ([]Point, string, error) { return parseGPX(r, MaxRawPoints) }

func parseGPX(r io.Reader, maxPts int) ([]Point, string, error) {
	g, err := gpx.Parse(r)
	if err != nil {
		return nil, "", err
	}
	var pts []Point
	name := ""
	add := func(src []gpx.GPXPoint) error {
		if len(pts)+len(src) > maxPts {
			return ErrTooManyPoints
		}
		for i := range src {
			ele := math.NaN()
			if src[i].Elevation.NotNull() {
				ele = src[i].Elevation.Value()
			}
			if p, ok := newPoint(src[i].Latitude, src[i].Longitude, ele); ok {
				pts = append(pts, p)
			}
		}
		return nil
	}
	for i := range g.Tracks {
		if name == "" {
			name = g.Tracks[i].Name
		}
		for j := range g.Tracks[i].Segments {
			if err := add(g.Tracks[i].Segments[j].Points); err != nil {
				return nil, "", err
			}
		}
	}
	if len(pts) == 0 {
		for i := range g.Routes {
			if err := add(g.Routes[i].Points); err != nil {
				return nil, "", err
			}
		}
	}
	if len(pts) == 0 {
		return nil, "", ErrNoTrack
	}
	if name == "" {
		name = g.Name
	}
	return pts, name, nil
}
