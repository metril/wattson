package route

import (
	"encoding/xml"
	"errors"
	"io"
	"math"
	"strconv"
)

const (
	tcxCapLat = iota + 1
	tcxCapLon
	tcxCapAlt
	tcxCapCourseName
	tcxCapNotes
)

// ParseTCX streams a TCX file: Courses/Course/Track/Trackpoint, else
// Activities/Activity/Lap/Track/Trackpoint. Points without Position are skipped.
// name is the course name, else the activity notes.
func ParseTCX(r io.Reader) ([]Point, string, error) { return parseTCX(r, MaxRawPoints) }

func parseTCX(r io.Reader, maxPts int) ([]Point, string, error) {
	dec := newXMLDecoder(r)
	var (
		course, activity []Point
		courseName       string
		activityNotes    string
		stack            elemStack
		capt             textCapture
		inCourse         int // open Course / Activity elements
		inActivity       int
		inPt             bool
		ptDepth          int
		ptInCourse       bool
		lat, lon, alt    float64
		gotLat, gotLon   bool
	)
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
			if err := stack.push(t.Name.Local); err != nil {
				return nil, "", err
			}
			parent := stack.parent()
			switch t.Name.Local {
			case "Course":
				inCourse++
			case "Activity":
				inActivity++
			case "Trackpoint":
				if !inPt && parent == "Track" && (inCourse > 0 || inActivity > 0) {
					inPt, ptDepth, ptInCourse = true, stack.depth(), inCourse > 0
					alt, gotLat, gotLon = math.NaN(), false, false
				}
			case "LatitudeDegrees":
				if inPt && parent == "Position" && stack.depth() == ptDepth+2 {
					capt.start(tcxCapLat, stack.depth())
				}
			case "LongitudeDegrees":
				if inPt && parent == "Position" && stack.depth() == ptDepth+2 {
					capt.start(tcxCapLon, stack.depth())
				}
			case "AltitudeMeters":
				if inPt && stack.depth() == ptDepth+1 {
					capt.start(tcxCapAlt, stack.depth())
				}
			case "Name":
				if parent == "Course" && courseName == "" {
					capt.start(tcxCapCourseName, stack.depth())
				}
			case "Notes":
				if parent == "Activity" && activityNotes == "" {
					capt.start(tcxCapNotes, stack.depth())
				}
			}
		case xml.CharData:
			capt.add(stack.depth(), t)
		case xml.EndElement:
			kind, text := capt.finish(stack.depth())
			switch kind {
			case tcxCapLat, tcxCapLon, tcxCapAlt:
				v, err := strconv.ParseFloat(text, 64)
				if err != nil {
					break
				}
				switch kind {
				case tcxCapLat:
					lat, gotLat = v, true
				case tcxCapLon:
					lon, gotLon = v, true
				default:
					alt = v
				}
			case tcxCapCourseName:
				courseName = text
			case tcxCapNotes:
				activityNotes = text
			}
			switch t.Name.Local {
			case "Course":
				inCourse--
			case "Activity":
				inActivity--
			case "Trackpoint":
				if inPt && stack.depth() == ptDepth {
					inPt = false
					if !gotLat || !gotLon {
						break
					}
					if p, ok := newPoint(lat, lon, alt); ok {
						if len(course)+len(activity) >= maxPts {
							return nil, "", ErrTooManyPoints
						}
						if ptInCourse {
							course = append(course, p)
						} else {
							activity = append(activity, p)
						}
					}
				}
			}
			stack.pop()
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
