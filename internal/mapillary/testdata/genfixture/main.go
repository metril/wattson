// Command genfixture writes the Mapillary fixtures the e2e fake serves: z14
// coverage tiles with one image every 20 m along a GPX route, on the route's
// heading (ids 1000, 1001, ...), plus img.jpg, a 64x48 solid JPEG.
//
//	go run ./internal/mapillary/testdata/genfixture -gpx internal/route/testdata/ramp.gpx -out web/e2e/fixtures/mly
package main

import (
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
)

const (
	zoom       = 14
	spacingM   = 20.0
	firstID    = 1000
	capturedMS = 1_700_000_000_000
	earthR     = 6_371_000.0
)

func main() {
	gpx := flag.String("gpx", "internal/route/testdata/ramp.gpx", "route to cover")
	out := flag.String("out", "web/e2e/fixtures/mly", "output directory")
	flag.Parse()
	if err := run(*gpx, *out); err != nil {
		fmt.Fprintln(os.Stderr, "genfixture:", err)
		os.Exit(1)
	}
}

func readGPX(path string) ([]orb.Point, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Pts []struct {
			Lat float64 `xml:"lat,attr"`
			Lon float64 `xml:"lon,attr"`
		} `xml:"trk>trkseg>trkpt"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	pts := make([]orb.Point, len(doc.Pts))
	for i, p := range doc.Pts {
		pts[i] = orb.Point{p.Lon, p.Lat}
	}
	return pts, nil
}

func rad(d float64) float64 { return d * math.Pi / 180 }

func distM(a, b orb.Point) float64 {
	dLat, dLon := rad(b[1]-a[1]), rad(b[0]-a[0])
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(rad(a[1]))*math.Cos(rad(b[1]))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthR * math.Asin(math.Sqrt(h))
}

func bearing(a, b orb.Point) float64 {
	y := math.Sin(rad(b[0]-a[0])) * math.Cos(rad(b[1]))
	x := math.Cos(rad(a[1]))*math.Sin(rad(b[1])) - math.Sin(rad(a[1]))*math.Cos(rad(b[1]))*math.Cos(rad(b[0]-a[0]))
	return math.Mod(math.Atan2(y, x)*180/math.Pi+360, 360)
}

type image1 struct {
	id      int64
	p       orb.Point
	compass float64
}

func sample(pts []orb.Point) []image1 {
	var out []image1
	id := int64(firstID)
	carry := 0.0 // metres until the next image
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		seg := distM(a, b)
		if seg == 0 {
			continue
		}
		hd := bearing(a, b)
		for d := carry; d <= seg; d += spacingM {
			f := d / seg
			out = append(out, image1{id, orb.Point{a[0] + (b[0]-a[0])*f, a[1] + (b[1]-a[1])*f}, hd})
			id++
			carry = d + spacingM - seg
		}
		if carry < 0 || carry > spacingM {
			carry = 0
		}
	}
	return out
}

func run(gpxPath, outDir string) error {
	pts, err := readGPX(gpxPath)
	if err != nil {
		return err
	}
	imgs := sample(pts)
	if len(imgs) == 0 {
		return fmt.Errorf("%s has no route to cover", gpxPath)
	}
	byTile := map[maptile.Tile]*geojson.FeatureCollection{}
	for _, im := range imgs {
		t := maptile.At(im.p, zoom)
		fc := byTile[t]
		if fc == nil {
			fc = geojson.NewFeatureCollection()
			byTile[t] = fc
		}
		ft := geojson.NewFeature(im.p)
		ft.Properties["id"] = im.id
		ft.Properties["compass_angle"] = im.compass
		ft.Properties["captured_at"] = int64(capturedMS) + im.id*1000
		ft.Properties["is_pano"] = false
		fc.Append(ft)
	}
	for t, fc := range byTile {
		layers := mvt.NewLayers(map[string]*geojson.FeatureCollection{"image": fc})
		layers.ProjectToTile(t)
		b, err := mvt.Marshal(layers)
		if err != nil {
			return err
		}
		dir := filepath.Join(outDir, fmt.Sprint(zoom), fmt.Sprint(t.X))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(t.Y)), b, 0o644); err != nil {
			return err
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: 60, G: 110, B: 170, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "img.jpg"), buf.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %d images in %d tiles to %s\n", len(imgs), len(byTile), outDir)
	return nil
}
