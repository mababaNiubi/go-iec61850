// SPDX-License-Identifier: GPL-3.0-or-later
package goose

import (
	"github.com/mababaNiubi/go-iec61850/data"
	"testing"
)

func TestLayoutFieldEditPreservesOtherMembers(t *testing.T) {
	typ := data.TypeSpec{Kind: data.KindStructure, Components: []data.Component{{Name: "value", Type: data.TypeSpec{Kind: data.KindInteger, Width: 32}}, {Name: "enabled", Type: data.TypeSpec{Kind: data.KindBoolean}}}}
	layout := Layout{Members: []Member{{Name: "GGIO1$ST$Ind", Domain: "local", Type: typ}}}
	field, err := layout.Field("GGIO1$ST$Ind", "value")
	if err != nil {
		t.Fatal(err)
	}
	before := data.Structure(data.Integer(7), data.Boolean(true))
	values := []data.Value{before}
	if err := field.Set(values, data.Integer(99)); err != nil {
		t.Fatal(err)
	}
	value, err := field.Read(values)
	if err != nil || !value.Equal(data.Integer(99)) {
		t.Fatal(value, err)
	}
	children, _ := before.Elements()
	if !children[0].Equal(data.Integer(7)) {
		t.Fatal("original value changed")
	}
	children, _ = values[0].Elements()
	if !children[1].Equal(data.Boolean(true)) {
		t.Fatal("unselected sibling changed")
	}
	count, err := layout.LeafCount()
	if err != nil || count != 2 {
		t.Fatal(count, err)
	}
	other := Layout{Members: []Member{{Name: "GGIO1$ST$Ind", Domain: "local", Type: data.CloneType(typ)}}}
	if err := layout.CheckCompatible(other); err != nil {
		t.Fatal(err)
	}
	other.Members[0].Domain = "relative:OtherLD"
	if err := layout.CheckCompatible(other); err == nil {
		t.Fatal("another logical-device relationship accepted")
	}
}
