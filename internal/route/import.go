package route

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// Format is a supported route file format.
type Format string

const (
	FormatGPX Format = "gpx"
	FormatTCX Format = "tcx"
	FormatFIT Format = "fit"
)

var (
	ErrUnsupportedFormat = errors.New("route: unsupported file format")
	ErrNoTrack           = errors.New("route: no track points in file")
	ErrTooManyPoints     = errors.New("route: too many track points")
	ErrTooLarge          = errors.New("route: file too large")
)

const (
	// MaxRawPoints bounds the points accepted from one upload.
	MaxRawPoints = 500_000
	// MaxFileBytes bounds the bytes read from one upload.
	MaxFileBytes = 64 << 20
)

// Detect picks the format by content first, then by file extension.
func Detect(filename string, head []byte) (Format, error) {
	if len(head) >= 12 && string(head[8:12]) == ".FIT" {
		return FormatFIT, nil
	}
	switch {
	case bytes.Contains(head, []byte("<gpx")):
		return FormatGPX, nil
	case bytes.Contains(head, []byte("<TrainingCenterDatabase")):
		return FormatTCX, nil
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".gpx":
		return FormatGPX, nil
	case ".tcx":
		return FormatTCX, nil
	case ".fit":
		return FormatFIT, nil
	}
	return "", ErrUnsupportedFormat
}

// Parse reads at most MaxFileBytes from r and returns at most MaxRawPoints
// points. Malformed input yields an error, never a panic.
func Parse(f Format, r io.Reader) (pts []Point, name string, err error) {
	return parse(f, r, limits{MaxRawPoints, MaxFileBytes})
}

// limits are the upload bounds; tests lower them to stay fast.
type limits struct {
	points int
	bytes  int64
}

func parse(f Format, r io.Reader, lim limits) (pts []Point, name string, err error) {
	defer func() {
		if p := recover(); p != nil {
			pts, name, err = nil, "", fmt.Errorf("route: malformed %s file: %v", f, p)
		}
	}()
	lr := &limitedReader{r: r, n: lim.bytes}
	switch f {
	case FormatGPX:
		pts, name, err = parseGPX(lr, lim.points)
	case FormatTCX:
		pts, name, err = parseTCX(lr, lim.points)
	case FormatFIT:
		pts, name, err = parseFIT(lr, lim.points)
	default:
		return nil, "", ErrUnsupportedFormat
	}
	if lr.exceeded {
		return nil, "", ErrTooLarge
	}
	if err != nil {
		return nil, "", err
	}
	return pts, name, nil
}

// limitedReader fails (rather than silently truncating) past n bytes.
type limitedReader struct {
	r        io.Reader
	n        int64
	exceeded bool
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.exceeded {
		return 0, ErrTooLarge
	}
	if l.n <= 0 {
		// Allow a clean EOF at exactly the limit.
		var one [1]byte
		if n, _ := l.r.Read(one[:]); n > 0 {
			l.exceeded = true
			return 0, ErrTooLarge
		}
		return 0, io.EOF
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	return n, err
}
