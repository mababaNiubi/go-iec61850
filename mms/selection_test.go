// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"bytes"
	"context"
	"testing"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

func TestNestedAlternateAccessUsesASN1ChoiceTags(t *testing.T) {
	index := uint32(9)
	steps := []Selector{{Index: &index}, {Component: "cVal"}}
	// selectAlternateAccess[0], index[1], alternateAccess SEQUENCE,
	// final selectAccess.component[1] -- from the MMS ASN.1 declaration.
	want := []byte{0xa0, 0x0b, 0x81, 0x01, 0x09, 0x30, 0x06, 0x81, 0x04, 'c', 'V', 'a', 'l'}
	if got := encodeSelection(steps); !bytes.Equal(got, want) {
		t.Fatalf("wire=%x want=%x", got, want)
	}
	e, rest, err := ber.Parse(want)
	if err != nil || len(rest) != 0 {
		t.Fatal(err)
	}
	decoded, err := decodeSelection(e, 0)
	if err != nil || !(Selection{Path: steps}).Equal(Selection{Path: decoded}) {
		t.Fatal(decoded, err)
	}
	clone := (Selection{Path: steps}).Clone()
	*clone.Path[0].Index = 3
	if index != 9 {
		t.Fatal("selection clone aliases caller memory")
	}
	model := NewModel()
	name := DomainName("LD", "a")
	spec := TypeSpec{Kind: KindStructure, Components: []Component{{Name: "cVal", Type: TypeSpec{Kind: KindInteger, Width: 32}}}}
	if err = model.Add(Variable{Name: name, Writable: true, Type: TypeSpec{Kind: KindArray, Length: 2, Element: &spec}, Value: Array(Structure(Integer(1)), Structure(Integer(2)))}); err != nil {
		t.Fatal(err)
	}
	index = 1
	access := Access(name)
	access.Selection.Path = []Selector{{Index: &index}, {Component: "cVal"}}
	if err = model.Write(context.Background(), access, Integer(99)); err != nil {
		t.Fatal(err)
	}
	value, err := model.Read(context.Background(), access)
	if err != nil || !value.Equal(Integer(99)) {
		t.Fatal(value, err)
	}
	index = 0
	value, err = model.Read(context.Background(), access)
	if err != nil || !value.Equal(Integer(1)) {
		t.Fatal("sibling changed", value, err)
	}
}
