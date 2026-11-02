package route

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"io"
	"math"
)

var errBadBlob = errors.New("route: invalid profile blob")

const (
	blobMagic      = "WRP1"
	maxBlobSamples = 200_000
	// 4 columns of at most 10-byte varints plus the header.
	maxBlobPayload = 4*10*maxBlobSamples + 64
)

// Encode serialises p: "WRP1" + flate(uvarint n, float64 StepM, float64
// LengthM, then four columns of zigzag-varint deltas: lat*1e7, lon*1e7,
// elevation in cm, grade in 0.01 %).
func Encode(p *Profile) []byte {
	n := p.Len()
	raw := make([]byte, 0, 20+n*6)
	raw = binary.AppendUvarint(raw, uint64(n))
	raw = binary.LittleEndian.AppendUint64(raw, math.Float64bits(p.StepM))
	raw = binary.LittleEndian.AppendUint64(raw, math.Float64bits(p.LengthM))
	for _, col := range []struct {
		v     []float64
		scale float64
	}{{p.Lat, 1e7}, {p.Lon, 1e7}, {p.Ele, 100}, {p.Grade, 100}} {
		var prev int64
		for _, x := range col.v {
			q := int64(math.Round(x * col.scale))
			raw = binary.AppendVarint(raw, q-prev)
			prev = q
		}
	}
	var out bytes.Buffer
	out.WriteString(blobMagic)
	w, _ := flate.NewWriter(&out, flate.BestSpeed) // level is valid: never errors
	_, _ = w.Write(raw)
	_ = w.Close()
	return out.Bytes()
}

// Decode parses a blob written by Encode. It rejects unknown versions and
// bounds the decompressed size.
func Decode(b []byte) (*Profile, error) {
	if len(b) < len(blobMagic) || string(b[:len(blobMagic)]) != blobMagic {
		return nil, errBadBlob
	}
	fr := flate.NewReader(bytes.NewReader(b[len(blobMagic):]))
	defer fr.Close()
	raw, err := io.ReadAll(io.LimitReader(fr, maxBlobPayload+1))
	if err != nil || len(raw) > maxBlobPayload {
		return nil, errBadBlob
	}
	n64, k := binary.Uvarint(raw)
	if k <= 0 || n64 < 2 || n64 > maxBlobSamples || len(raw)-k < 16 {
		return nil, errBadBlob
	}
	n := int(n64)
	raw = raw[k:]
	p := &Profile{
		StepM:   math.Float64frombits(binary.LittleEndian.Uint64(raw)),
		LengthM: math.Float64frombits(binary.LittleEndian.Uint64(raw[8:])),
	}
	raw = raw[16:]
	if !(p.StepM > 0) || !(p.LengthM > 0) || math.IsInf(p.StepM, 0) || math.IsInf(p.LengthM, 0) {
		return nil, errBadBlob
	}
	for _, col := range []struct {
		dst   *[]float64
		scale float64
	}{{&p.Lat, 1e7}, {&p.Lon, 1e7}, {&p.Ele, 100}, {&p.Grade, 100}} {
		v := make([]float64, n)
		var acc int64
		for i := range v {
			d, k := binary.Varint(raw)
			if k <= 0 {
				return nil, errBadBlob
			}
			raw = raw[k:]
			acc += d
			v[i] = float64(acc) / col.scale
		}
		*col.dst = v
	}
	if len(raw) != 0 {
		return nil, errBadBlob
	}
	return p, nil
}
