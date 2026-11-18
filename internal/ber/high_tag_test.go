// SPDX-License-Identifier: GPL-3.0-or-later
package ber

import (
	"bytes"
	"testing"
)

// MMS file services use high tag numbers 72-77, encoded as two identifier
// octets. The parser must represent them without disturbing the single-octet
// form every other service uses.
func TestHighTagRoundTrip(t *testing.T) {
	cases := []struct {
		first, second byte
	}{
		{0xbf, 0x48}, // FileOpen request/response
		{0x9f, 0x49}, // FileRead request
		{0x9f, 0x4a}, // FileClose request
		{0xbf, 0x4d}, // FileDirectory request/response
	}
	for _, c := range cases {
		raw := EncodeHigh(c.first, c.second, []byte("body"))
		e, rest, err := Parse(raw)
		if err != nil {
			t.Fatalf("%02x%02x: %v", c.first, c.second, err)
		}
		if len(rest) != 0 {
			t.Fatalf("%02x%02x: unconsumed bytes", c.first, c.second)
		}
		if e.Tag != c.first || e.High != c.second || !bytes.Equal(e.Bytes, []byte("body")) {
			t.Fatalf("%02x%02x: got tag %02x high %02x body %q", c.first, c.second, e.Tag, e.High, e.Bytes)
		}
	}
}

func TestHighTagIndefiniteLength(t *testing.T) {
	raw := []byte{0xbf, 0x48, 0x80, 0x02, 0x01, 0x01, 0x00, 0x00}
	e, rest, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 || e.Tag != 0xbf || e.High != 0x48 || !bytes.Equal(e.Bytes, raw[3:6]) {
		t.Fatalf("unexpected element %+v rest % x", e, rest)
	}
}

func TestHighTagRejectsUnsupportedForms(t *testing.T) {
	cases := map[string][]byte{
		"multi-octet tag": {0xbf, 0x81, 0x00, 0x00},
		"non-minimal":     {0xbf, 0x05, 0x00},
		"truncated":       {0xbf},
	}
	for name, raw := range cases {
		if _, _, err := Parse(raw); err == nil {
			t.Fatalf("%s: expected a rejection", name)
		}
	}
}

// Low tags keep High zero so existing comparisons on Tag alone stay valid.
func TestLowTagKeepsHighZero(t *testing.T) {
	e, _, err := Parse(Encode(0xa4, []byte{0x01}))
	if err != nil {
		t.Fatal(err)
	}
	if e.Tag != 0xa4 || e.High != 0 {
		t.Fatalf("unexpected element %+v", e)
	}
}
