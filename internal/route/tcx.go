package route

import (
	"encoding/xml"
	"errors"
	"io"
	"math"
	"strings"
)

type tcxTrackpoint struct {
	Position *struct {
		Lat *float64 `xml:"LatitudeDegrees"`
		Lon *float64 `xml:"LongitudeDegrees"`
	} `xml:"Position"`
	Altitude *float64 `xml:"AltitudeMeters"`
}

// ParseTCX streams a TCX file: Courses/Course/Track/Trackpoint, else
// Activities/Activity/Lap/Track/Trackpoint. Points without Position are skipped.
// name is the course name, else the activity notes.
func ParseTCX(r io.Reader) ([]Point, string, error) { return parseTCX(r, MaxRawPoints) }

func parseTCX(r io.Reader, maxPts int) ([]Point, string, error) {
	dec := xml.NewDecoder(r)
	var (
		course, activity []Point
		courseName       string
		activityNotes    string
		stack            []string // local names of open elements
	)
	inside := func(n string) bool {
		for _, s := range stack {
			if s == n {
				return true
			}
		}
		return false
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Trackpoint":
				var tp tcxTrackpoint
				if err := dec.DecodeElement(&tp, &t); err != nil {
					return nil, "", err
				}
				if tp.Position == nil || tp.Position.Lat == nil || tp.Position.Lon == nil {
					continue
				}
				ele := math.NaN()
				if tp.Altitude != nil {
					ele = *tp.Altitude
				}
				p, ok := newPoint(*tp.Position.Lat, *tp.Position.Lon, ele)
				if !ok {
					continue
				}
				if len(course)+len(activity) >= maxPts {
					return nil, "", ErrTooManyPoints
				}
				if inside("Course") {
					course = append(course, p)
				} else if inside("Activity") {
					activity = append(activity, p)
				}
				continue // DecodeElement consumed the end element
			case "Name", "Notes":
				if n := len(stack); n > 0 && courseName == "" && t.Name.Local == "Name" && stack[n-1] == "Course" ||
					n > 0 && activityNotes == "" && t.Name.Local == "Notes" && stack[n-1] == "Activity" {
					var s string
					if err := dec.DecodeElement(&s, &t); err != nil {
						return nil, "", err
					}
					s = strings.TrimSpace(s)
					if t.Name.Local == "Name" {
						courseName = s
					} else {
						activityNotes = s
					}
					continue
				}
			}
			stack = append(stack, t.Name.Local)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if len(course) > 0 {
		return course, courseName, nil
	}
	if len(activity) > 0 {
		return activity, activityNotes, nil
	}
	return nil, "", ErrNoTrack
}
