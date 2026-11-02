package route

import (
	"encoding/xml"
	"errors"
	"io"
	"math"
	"strconv"
)

const (
	gpxCapEle = iota + 1
	gpxCapTrkName
	gpxCapMetaName
)

// ParseGPX streams a GPX 1.0/1.1 file and returns all trk/trkseg points in
// order, else the rte points. name is the first track name, else the
// metadata name.
func ParseGPX(r io.Reader) ([]Point, string, error) { return parseGPX(r, MaxRawPoints) }

func parseGPX(r io.Reader, maxPts int) ([]Point, string, error) {
	dec := newXMLDecoder(r)
	var (
		trk, rte         []Point
		trkName, metaNam string
		stack            elemStack
		capt             textCapture
		inPt, ptIsTrk    bool // inside a trkpt/rtept
		ptDepth          int
		ptValid          bool
		lat, lon, ele    float64
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
			case "trkpt", "rtept":
				isTrk := t.Name.Local == "trkpt"
				if inPt || (isTrk && parent != "trkseg") || (!isTrk && parent != "rte") {
					break
				}
				inPt, ptIsTrk, ptDepth, ele = true, isTrk, stack.depth(), math.NaN()
				var gotLat, gotLon bool
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "lat":
						lat, err = strconv.ParseFloat(a.Value, 64)
						gotLat = err == nil
					case "lon":
						lon, err = strconv.ParseFloat(a.Value, 64)
						gotLon = err == nil
					}
				}
				ptValid = gotLat && gotLon
			case "ele":
				if inPt && stack.depth() == ptDepth+1 {
					capt.start(gpxCapEle, stack.depth())
				}
			case "name":
				switch {
				case parent == "trk" && trkName == "":
					capt.start(gpxCapTrkName, stack.depth())
				case (parent == "metadata" || parent == "gpx") && metaNam == "":
					capt.start(gpxCapMetaName, stack.depth())
				}
			}
		case xml.CharData:
			capt.add(stack.depth(), t)
		case xml.EndElement:
			switch kind, text := capt.finish(stack.depth()); kind {
			case gpxCapEle:
				if v, err := strconv.ParseFloat(text, 64); err == nil {
					ele = v
				}
			case gpxCapTrkName:
				trkName = text
			case gpxCapMetaName:
				metaNam = text
			}
			if inPt && stack.depth() == ptDepth {
				inPt = false
				if ptValid {
					if len(trk)+len(rte) >= maxPts {
						return nil, "", ErrTooManyPoints
					}
					if p, ok := newPoint(lat, lon, ele); ok {
						if ptIsTrk {
							trk = append(trk, p)
						} else {
							rte = append(rte, p)
						}
					}
				}
			}
			stack.pop()
		}
	}
	name := trkName
	if name == "" {
		name = metaNam
	}
	switch {
	case len(trk) > 0:
		return trk, name, nil
	case len(rte) > 0:
		return rte, name, nil
	}
	return nil, "", ErrNoTrack
}
