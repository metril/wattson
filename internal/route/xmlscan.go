package route

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// maxXMLDepth bounds element nesting in untrusted XML.
const maxXMLDepth = 64

// maxTextBytes bounds captured character data (names, numbers).
const maxTextBytes = 4096

var errTooDeep = errors.New("route: XML nesting too deep")

// newXMLDecoder returns a strict decoder that also reads Latin-1 files.
func newXMLDecoder(r io.Reader) *xml.Decoder {
	d := xml.NewDecoder(r)
	d.CharsetReader = func(label string, in io.Reader) (io.Reader, error) {
		switch strings.ToLower(label) {
		case "utf-8", "utf8", "us-ascii", "ascii":
			return in, nil
		case "iso-8859-1", "latin1", "windows-1252":
			return &latin1Reader{r: in}, nil
		}
		return nil, errors.New("route: unsupported XML charset " + label)
	}
	return d
}

// latin1Reader converts ISO-8859-1 bytes to UTF-8.
type latin1Reader struct {
	r   io.Reader
	out []byte
}

func (l *latin1Reader) Read(p []byte) (int, error) {
	for len(l.out) == 0 {
		var in [256]byte
		n, err := l.r.Read(in[:])
		for _, b := range in[:n] {
			l.out = utf8.AppendRune(l.out, rune(b))
		}
		if len(l.out) == 0 {
			return 0, err // n == 0 here, so err is the only news
		}
		if err != nil && err != io.EOF {
			return 0, err
		}
	}
	n := copy(p, l.out)
	l.out = l.out[n:]
	return n, nil
}

// elemStack tracks open elements without ever scanning them.
type elemStack struct{ names []string }

func (s *elemStack) push(name string) error {
	if len(s.names) >= maxXMLDepth {
		return errTooDeep
	}
	s.names = append(s.names, name)
	return nil
}

func (s *elemStack) pop() {
	if len(s.names) > 0 {
		s.names = s.names[:len(s.names)-1]
	}
}

func (s *elemStack) depth() int { return len(s.names) }

// parent is the name of the element enclosing the current one ("" at the root).
// Call it after push.
func (s *elemStack) parent() string {
	if len(s.names) < 2 {
		return ""
	}
	return s.names[len(s.names)-2]
}

// textCapture collects the direct character data of one element.
type textCapture struct {
	kind  int // 0 = off
	depth int // stack depth of the captured element
	buf   []byte
}

func (c *textCapture) start(kind, depth int) { c.kind, c.depth, c.buf = kind, depth, c.buf[:0] }

func (c *textCapture) add(depth int, b []byte) {
	if c.kind != 0 && depth == c.depth && len(c.buf) < maxTextBytes {
		c.buf = append(c.buf, b[:min(len(b), maxTextBytes-len(c.buf))]...)
	}
}

// finish reports the captured kind and text when the element at depth ends.
func (c *textCapture) finish(depth int) (kind int, text string) {
	if c.kind == 0 || depth != c.depth {
		return 0, ""
	}
	kind, text = c.kind, strings.TrimSpace(string(c.buf))
	c.kind = 0
	return kind, text
}
