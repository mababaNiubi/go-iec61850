// SPDX-License-Identifier: GPL-3.0-or-later
package sv

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
)

// SampleData is a mutable, externally described SV seqData buffer. Field access
// uses byte offsets and network byte order. No ASN.1 type tags are inserted.
type SampleData []byte

// NewSampleData allocates a zeroed seqData buffer of size bytes, in 0..MaxAPDUSize.
func NewSampleData(size int) (SampleData, error) {
	if size < 0 || size > MaxAPDUSize {
		return nil, fmt.Errorf("sv: invalid sample data size")
	}
	return make(SampleData, size), nil
}

// Bytes returns an independent copy of the mutable seqData buffer.
func (d SampleData) Bytes() []byte { return bytes.Clone(d) }
func (d SampleData) bounds(offset, width int) ([]byte, error) {
	if offset < 0 || offset > len(d) || width > len(d)-offset {
		return nil, fmt.Errorf("sv: sample offset %d width %d outside %d bytes", offset, width, len(d))
	}
	return d[offset : offset+width], nil
}

// Uint8 reads a 8-bit value at a byte offset in network byte order. Out-of-bounds access
// returns an error.
func (d SampleData) Uint8(offset int) (uint8, error) {
	b, e := d.bounds(offset, 1)
	if e != nil {
		return 0, e
	}
	return b[0], nil
}

// Int8 reads a 8-bit value at a byte offset in network byte order. Out-of-bounds access
// returns an error.
func (d SampleData) Int8(offset int) (int8, error) { v, e := d.Uint8(offset); return int8(v), e }

// Uint16 reads a 16-bit value at a byte offset in network byte order. Out-of-bounds access
// returns an error.
func (d SampleData) Uint16(offset int) (uint16, error) {
	b, e := d.bounds(offset, 2)
	if e != nil {
		return 0, e
	}
	return binary.BigEndian.Uint16(b), nil
}

// Int16 reads a 16-bit value at a byte offset in network byte order. Out-of-bounds access
// returns an error.
func (d SampleData) Int16(offset int) (int16, error) { v, e := d.Uint16(offset); return int16(v), e }

// Uint32 reads a 32-bit value at a byte offset in network byte order. Out-of-bounds access
// returns an error.
func (d SampleData) Uint32(offset int) (uint32, error) {
	b, e := d.bounds(offset, 4)
	if e != nil {
		return 0, e
	}
	return binary.BigEndian.Uint32(b), nil
}

// Int32 reads a 32-bit value at a byte offset in network byte order. Out-of-bounds access
// returns an error.
func (d SampleData) Int32(offset int) (int32, error) { v, e := d.Uint32(offset); return int32(v), e }

// Uint64 reads a 64-bit value at a byte offset in network byte order. Out-of-bounds access
// returns an error.
func (d SampleData) Uint64(offset int) (uint64, error) {
	b, e := d.bounds(offset, 8)
	if e != nil {
		return 0, e
	}
	return binary.BigEndian.Uint64(b), nil
}

// Int64 reads a 64-bit value at a byte offset in network byte order. Out-of-bounds access
// returns an error.
func (d SampleData) Int64(offset int) (int64, error) { v, e := d.Uint64(offset); return int64(v), e }

// Float32 reads a 32-bit value at a byte offset in network byte order. Out-of-bounds access
// returns an error.
func (d SampleData) Float32(offset int) (float32, error) {
	v, e := d.Uint32(offset)
	return math.Float32frombits(v), e
}

// Float64 reads a 64-bit value at a byte offset in network byte order. Out-of-bounds access
// returns an error.
func (d SampleData) Float64(offset int) (float64, error) {
	v, e := d.Uint64(offset)
	return math.Float64frombits(v), e
}

// Quality reads an unmodified four-octet SV Quality word at a byte offset. It checks bounds
// but does not decide application usability.
func (d SampleData) Quality(offset int) (uint32, error) { return d.Uint32(offset) }

// UTCAt reads eight raw UTC octets at a byte offset, preserving fraction and time-quality
// bits. Out-of-bounds access returns an error.
func (d SampleData) UTCAt(offset int) ([8]byte, error) {
	var v [8]byte
	b, e := d.bounds(offset, 8)
	if e != nil {
		return v, e
	}
	copy(v[:], b)
	return v, nil
}

// SetUint8 writes a 8-bit value at a byte offset in network byte order. Out-of-bounds
// access returns an error.
func (d SampleData) SetUint8(offset int, v uint8) error {
	b, e := d.bounds(offset, 1)
	if e != nil {
		return e
	}
	b[0] = v
	return nil
}

// SetInt8 writes a 8-bit value at a byte offset in network byte order. Out-of-bounds access
// returns an error.
func (d SampleData) SetInt8(offset int, v int8) error { return d.SetUint8(offset, uint8(v)) }

// SetUint16 writes a 16-bit value at a byte offset in network byte order. Out-of-bounds
// access returns an error.
func (d SampleData) SetUint16(offset int, v uint16) error {
	b, e := d.bounds(offset, 2)
	if e != nil {
		return e
	}
	binary.BigEndian.PutUint16(b, v)
	return nil
}

// SetInt16 writes a 16-bit value at a byte offset in network byte order. Out-of-bounds
// access returns an error.
func (d SampleData) SetInt16(offset int, v int16) error { return d.SetUint16(offset, uint16(v)) }

// SetUint32 writes a 32-bit value at a byte offset in network byte order. Out-of-bounds
// access returns an error.
func (d SampleData) SetUint32(offset int, v uint32) error {
	b, e := d.bounds(offset, 4)
	if e != nil {
		return e
	}
	binary.BigEndian.PutUint32(b, v)
	return nil
}

// SetInt32 writes a 32-bit value at a byte offset in network byte order. Out-of-bounds
// access returns an error.
func (d SampleData) SetInt32(offset int, v int32) error { return d.SetUint32(offset, uint32(v)) }

// SetUint64 writes a 64-bit value at a byte offset in network byte order. Out-of-bounds
// access returns an error.
func (d SampleData) SetUint64(offset int, v uint64) error {
	b, e := d.bounds(offset, 8)
	if e != nil {
		return e
	}
	binary.BigEndian.PutUint64(b, v)
	return nil
}

// SetInt64 writes a 64-bit value at a byte offset in network byte order. Out-of-bounds
// access returns an error.
func (d SampleData) SetInt64(offset int, v int64) error { return d.SetUint64(offset, uint64(v)) }

// SetFloat32 writes a 32-bit value at a byte offset in network byte order. Out-of-bounds
// access returns an error.
func (d SampleData) SetFloat32(offset int, v float32) error {
	return d.SetUint32(offset, math.Float32bits(v))
}

// SetFloat64 writes a 64-bit value at a byte offset in network byte order. Out-of-bounds
// access returns an error.
func (d SampleData) SetFloat64(offset int, v float64) error {
	return d.SetUint64(offset, math.Float64bits(v))
}

// SetQuality writes an unmodified four-octet SV Quality word at a byte offset. It checks
// bounds but does not decide application usability.
func (d SampleData) SetQuality(offset int, v uint32) error { return d.SetUint32(offset, v) }

// SetUTCAt writes eight raw UTC octets at a byte offset, preserving fraction and
// time-quality bits. Out-of-bounds access returns an error.
func (d SampleData) SetUTCAt(offset int, v [8]byte) error {
	b, e := d.bounds(offset, 8)
	if e != nil {
		return e
	}
	copy(b, v[:])
	return nil
}

// Int32Quality is one fixed-layout measurement followed by an unmodified
// four-octet quality word. This helper does not infer a dataset/SCL profile.
type Int32Quality struct {
	Value   int32
	Quality uint32
}

// EncodeInt32Quality encodes ordered int32/quality pairs, eight network-order octets per
// measurement. It does not infer scaling or an SCL dataset.
func EncodeInt32Quality(values []Int32Quality) ([]byte, error) {
	if len(values) > MaxAPDUSize/8 {
		return nil, fmt.Errorf("sv: measurement array too large")
	}
	out := make([]byte, len(values)*8)
	for i, v := range values {
		binary.BigEndian.PutUint32(out[i*8:], uint32(v.Value))
		binary.BigEndian.PutUint32(out[i*8+4:], v.Quality)
	}
	return out, nil
}

// DecodeInt32Quality decodes a bounded payload whose length is a multiple of eight,
// preserving raw quality bits.
func DecodeInt32Quality(encoded []byte) ([]Int32Quality, error) {
	if len(encoded) > MaxAPDUSize || len(encoded)%8 != 0 {
		return nil, fmt.Errorf("sv: Int32/Quality payload length must be a multiple of eight")
	}
	out := make([]Int32Quality, len(encoded)/8)
	for i := range out {
		out[i] = Int32Quality{int32(binary.BigEndian.Uint32(encoded[i*8:])), binary.BigEndian.Uint32(encoded[i*8+4:])}
	}
	return out, nil
}
