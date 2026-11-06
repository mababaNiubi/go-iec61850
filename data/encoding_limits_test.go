// SPDX-License-Identifier: GPL-3.0-or-later
package data

import (
	"testing"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

func TestEncodeValueGlobalNodeBudget(t *testing.T) {
	children := make([]Value, ber.MaxElements)
	for i := range children {
		children[i] = Boolean(false)
	}
	// Include the enclosing array in the same budget as its children.
	valid := Array(children[:ber.MaxElements-1]...)
	encoded, err := EncodeValue(valid)
	if err != nil {
		t.Fatalf("maximum legal node count rejected: %v", err)
	}
	decoded, err := DecodeValue(encoded)
	if err != nil || !valid.Equal(decoded) {
		t.Fatalf("maximum legal node count did not round-trip: %v", err)
	}
	if _, err := EncodeValue(Array(children...)); err == nil {
		t.Fatal("encoded a tree with more nodes than DecodeValue accepts")
	}
	// Neither child independently exceeds the budget; the complete tree does.
	branch := Array(children[:ber.MaxElements/2-1]...)
	if _, err := EncodeValue(Structure(branch, branch)); err == nil {
		t.Fatal("encoder reset the node budget for each composite branch")
	}
}

func TestBitStringPayloadIncludesUnusedBitCount(t *testing.T) {
	raw := make([]byte, ber.MaxElementSize)
	tooLarge, err := BitString(raw, len(raw)*8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EncodeValue(tooLarge); err == nil {
		t.Fatal("encoded bit-string content beyond the BER payload limit")
	}
	valid, err := BitString(raw[:len(raw)-1], (len(raw)-1)*8)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeValue(valid)
	if err != nil {
		t.Fatalf("maximum legal bit-string payload rejected: %v", err)
	}
	decoded, err := DecodeValue(encoded)
	if err != nil || !valid.Equal(decoded) {
		t.Fatalf("maximum legal bit-string payload did not round-trip: %v", err)
	}
}
