package route

import (
	"io"
	"math"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/untyped/mesgnum"
	"github.com/muktihari/fit/proto"
)

// fitCollector gathers record positions as the decoder streams messages.
type fitCollector struct {
	max     int
	pts     []Point
	name    string
	tooMany bool
}

func (c *fitCollector) OnMesg(m proto.Message) {
	switch m.Num {
	case mesgnum.Course:
		if c.name == "" {
			c.name = mesgdef.NewCourse(&m).Name
		}
	case mesgnum.Record:
		rec := mesgdef.NewRecord(&m)
		// Position invalid sentinels are checked on the raw semicircles.
		if rec.PositionLat == math.MaxInt32 || rec.PositionLong == math.MaxInt32 {
			return
		}
		if len(c.pts) >= c.max {
			c.tooMany = true
			return
		}
		ele := rec.EnhancedAltitudeScaled()
		if rec.EnhancedAltitude == math.MaxUint32 {
			ele = rec.AltitudeScaled()
			if rec.Altitude == math.MaxUint16 {
				ele = math.NaN()
			}
		}
		if p, ok := newPoint(rec.PositionLatDegrees(), rec.PositionLongDegrees(), ele); ok {
			c.pts = append(c.pts, p)
		}
	}
}

// abortReader stops the decoder as soon as the collector overflows.
type abortReader struct {
	r io.Reader
	c *fitCollector
}

func (a abortReader) Read(p []byte) (int, error) {
	if a.c.tooMany {
		return 0, ErrTooManyPoints
	}
	return a.r.Read(p)
}

// ParseFIT reads record messages with a valid position (semicircles ->
// degrees); ele = enhanced_altitude, else altitude; name = course name.
func ParseFIT(r io.Reader) ([]Point, string, error) { return parseFIT(r, MaxRawPoints) }

func parseFIT(r io.Reader, maxPts int) ([]Point, string, error) {
	c := &fitCollector{max: maxPts}
	dec := decoder.New(abortReader{r, c}, decoder.WithMesgListener(c), decoder.WithBroadcastOnly())
	for dec.Next() {
		if _, err := dec.Decode(); err != nil {
			if c.tooMany {
				return nil, "", ErrTooManyPoints
			}
			return nil, "", err
		}
		if c.tooMany {
			return nil, "", ErrTooManyPoints
		}
	}
	if len(c.pts) == 0 {
		return nil, "", ErrNoTrack
	}
	return c.pts, c.name, nil
}
