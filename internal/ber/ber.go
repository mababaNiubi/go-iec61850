// SPDX-License-Identifier: GPL-3.0-or-later

// Package ber implements the bounded, single-octet-tag BER subset used by MMS.
// Encoders produce definite lengths. Decoders also accept constructed indefinite
// lengths. Returned element contents borrow the input buffer.
package ber

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	MaxElementSize = 16 << 20
	MaxDepth       = 64
	MaxElements    = 65536
)

// Element is one BER tag and its content, excluding its header and any EOC.
// High tag numbers use the two-octet identifier form: Tag keeps the leading
// octet (0xbf or 0x9f) and High holds the following tag-number octet. A zero
// High means the single-octet low form, so comparisons on Tag alone keep
// working for every existing MMS service.
type Element struct {
	Tag   byte
	High  byte
	Bytes []byte
}

// Parse reads exactly one element and returns the unconsumed input.
func Parse(b []byte) (Element, []byte, error) {
	e, n, err := parse(b, 0)
	if err != nil {
		return Element{}, nil, err
	}
	return e, b[n:], nil
}

func parse(b []byte, depth int) (Element, int, error) {
	if depth >= MaxDepth {
		return Element{}, 0, errors.New("ber: nesting limit exceeded")
	}
	if len(b) < 2 {
		return Element{}, 0, errors.New("ber: truncated header")
	}
	tag := b[0]
	if tag == 0 {
		return Element{}, 0, errors.New("ber: unexpected end-of-contents")
	}
	// High tag numbers carry a second identifier octet. Multi-octet tag
	// numbers (continuation bit set) are outside the MMS subset.
	var high byte
	header := 1
	if tag&0x1f == 0x1f {
		if len(b) < 3 {
			return Element{}, 0, errors.New("ber: truncated high tag")
		}
		if b[1]&0x80 != 0 {
			return Element{}, 0, errors.New("ber: multi-octet tag numbers are unsupported")
		}
		if b[1] < 0x1f {
			return Element{}, 0, errors.New("ber: non-minimal high tag form")
		}
		high = b[1]
		header = 2
	}
	pos, length := header+1, int(b[header])
	if length == 0x80 {
		if tag&0x20 == 0 {
			return Element{}, 0, errors.New("ber: primitive indefinite length")
		}
		for {
			if pos+2 > len(b) {
				return Element{}, 0, errors.New("ber: missing end-of-contents")
			}
			if b[pos] == 0 && b[pos+1] == 0 {
				return Element{tag, high, b[header+1 : pos]}, pos + 2, nil
			}
			_, n, err := parse(b[pos:], depth+1)
			if err != nil {
				return Element{}, 0, err
			}
			pos += n
			if pos-header-1 > MaxElementSize {
				return Element{}, 0, errors.New("ber: element size limit exceeded")
			}
		}
	}
	if length&0x80 != 0 {
		n := length & 0x7f
		if n == 0 || n > 4 || len(b)-pos < n {
			return Element{}, 0, errors.New("ber: invalid or truncated length")
		}
		var size uint64
		for _, c := range b[pos : pos+n] {
			size = size<<8 | uint64(c)
		}
		if size > MaxElementSize {
			return Element{}, 0, errors.New("ber: element size limit exceeded")
		}
		length = int(size)
		pos += n
	}
	if length > len(b)-pos {
		return Element{}, 0, errors.New("ber: truncated content")
	}
	return Element{tag, high, b[pos : pos+length]}, pos + length, nil
}

// ParseAll reads the sequence of elements in b.
func ParseAll(b []byte) ([]Element, error) {
	var elements []Element
	for len(b) != 0 {
		if len(elements) >= MaxElements {
			return nil, errors.New("ber: element count limit exceeded")
		}
		e, rest, err := Parse(b)
		if err != nil {
			return nil, err
		}
		elements = append(elements, e)
		b = rest
	}
	return elements, nil
}

// EncodeHigh encodes an element whose tag number needs the two-octet
// identifier form, such as the MMS file services (0xbf 0x48 … 0xbf 0x4d and
// 0x9f 0x49/0x4a). first carries the class/constructed bits; second carries
// the tag number and must be 31 or greater.
func EncodeHigh(first, second byte, content ...[]byte) []byte {
	if first&0x1f != 0x1f || second < 0x1f || second&0x80 != 0 {
		panic("ber: invalid high tag number")
	}
	length := 0
	for _, p := range content {
		length += len(p)
	}
	header := []byte{first, second}
	if length < 128 {
		header = append(header, byte(length))
	} else {
		var scratch [8]byte
		binary.BigEndian.PutUint64(scratch[:], uint64(length))
		pos := 0
		for scratch[pos] == 0 {
			pos++
		}
		header = append(header, 0x80|byte(8-pos))
		header = append(header, scratch[pos:]...)
	}
	result := make([]byte, len(header), len(header)+length)
	copy(result, header)
	for _, p := range content {
		result = append(result, p...)
	}
	return result
}

// Encode wraps concatenated content in a tag and definite-length header.
func Encode(tag byte, content ...[]byte) []byte {
	length := 0
	for _, p := range content {
		length += len(p)
	}
	header := []byte{tag}
	if length < 128 {
		header = append(header, byte(length))
	} else {
		var scratch [8]byte
		binary.BigEndian.PutUint64(scratch[:], uint64(length))
		first := 0
		for scratch[first] == 0 {
			first++
		}
		header = append(header, 0x80|byte(8-first))
		header = append(header, scratch[first:]...)
	}
	result := make([]byte, len(header), len(header)+length)
	copy(result, header)
	for _, p := range content {
		result = append(result, p...)
	}
	return result
}

// Uint encodes a nonnegative ASN.1 INTEGER, adding a sign octet when needed.
func Uint(tag byte, value uint64) []byte {
	var b [9]byte
	binary.BigEndian.PutUint64(b[1:], value)
	first := 1
	for first < 8 && b[first] == 0 {
		first++
	}
	if b[first]&0x80 != 0 {
		first--
	}
	return Encode(tag, b[first:])
}

// Int encodes a signed ASN.1 INTEGER in minimal two's-complement form.
func Int(tag byte, value int64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(value))
	first := 0
	for first < 7 && ((b[first] == 0 && b[first+1]&0x80 == 0) || (b[first] == 0xff && b[first+1]&0x80 != 0)) {
		first++
	}
	return Encode(tag, b[first:])
}

func DecodeUint(b []byte) (uint64, error) {
	if len(b) == 0 || b[0]&0x80 != 0 {
		return 0, errors.New("ber: empty or negative unsigned integer")
	}
	for len(b) > 1 && b[0] == 0 {
		b = b[1:]
	}
	if len(b) > 8 {
		return 0, errors.New("ber: unsigned integer exceeds 64 bits")
	}
	var value uint64
	for _, c := range b {
		value = value<<8 | uint64(c)
	}
	return value, nil
}

func DecodeInt(b []byte) (int64, error) {
	if len(b) == 0 {
		return 0, errors.New("ber: empty integer")
	}
	for len(b) > 1 && ((b[0] == 0 && b[1]&0x80 == 0) || (b[0] == 0xff && b[1]&0x80 != 0)) {
		b = b[1:]
	}
	if len(b) > 8 {
		return 0, fmt.Errorf("ber: signed integer exceeds 64 bits (%d octets)", len(b))
	}
	var value uint64
	if b[0]&0x80 != 0 {
		value = ^uint64(0)
	}
	for _, c := range b {
		value = value<<8 | uint64(c)
	}
	return int64(value), nil
}
