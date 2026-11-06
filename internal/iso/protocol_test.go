// SPDX-License-Identifier: GPL-3.0-or-later
package iso

import (
	"bytes"
	"testing"
)

func TestBERLengthCheckedBeforeIntConversion(t *testing.T) {
	lengths := []struct {
		name  string
		value []byte
	}{
		{"max-int32", []byte{0x7f, 0xff, 0xff, 0xff}},
		{"int32-sign-bit", []byte{0x80, 0, 0, 0}},
		{"uint32-max", []byte{0xff, 0xff, 0xff, 0xff}},
	}
	for _, test := range lengths {
		t.Run(test.name, func(t *testing.T) {
			encoded := append([]byte{0x61, 0x84}, test.value...)
			encoded = append(encoded, 0x30, 0)
			if _, _, err := readElement(encoded, 0); err == nil {
				t.Fatal("oversized declared length accepted")
			}
			// The same bound applies while searching for the end of an
			// enclosing constructed value with indefinite length.
			nested := append([]byte{0x31, 0x80}, encoded...)
			nested = append(nested, 0, 0)
			if _, _, err := readElement(nested, 0); err == nil {
				t.Fatal("oversized nested length accepted")
			}
		})
	}
	payload := bytes.Repeat([]byte{0x5a}, 128)
	valid := append([]byte{0x61, 0x81, 0x80}, payload...)
	valid = append(valid, 0xff)
	element, remaining, err := readElement(valid, 0)
	if err != nil || element.tag != 0x61 || !bytes.Equal(element.value, payload) || !bytes.Equal(remaining, []byte{0xff}) {
		t.Fatalf("valid long-form length did not preserve framing: %+v %x %v", element, remaining, err)
	}
}
