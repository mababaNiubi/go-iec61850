// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
)

func controlType() data.TypeSpec {
	return data.TypeSpec{Kind: data.KindStructure, Components: []data.Component{
		{Name: "ctlVal", Type: data.TypeSpec{Kind: data.KindBoolean}},
		{Name: "origin", Type: data.TypeSpec{Kind: data.KindStructure, Components: []data.Component{{Name: "orCat", Type: data.TypeSpec{Kind: data.KindInteger, Width: 8}}, {Name: "orIdent", Type: data.TypeSpec{Kind: data.KindOctetString, Length: -64}}}}},
		{Name: "ctlNum", Type: data.TypeSpec{Kind: data.KindUnsigned, Width: 8}},
		{Name: "T", Type: data.TypeSpec{Kind: data.KindUTC}},
		{Name: "Test", Type: data.TypeSpec{Kind: data.KindBoolean}},
		{Name: "Check", Type: data.TypeSpec{Kind: data.KindBitString, Length: -2}},
	}}
}
func TestQualityPreservesIECFlagNumbers(t *testing.T) {
	for _, q := range []Quality{0, 1, 2, 3, Overflow, OldData, Substituted, Test, OperatorBlocked, 0x1fff} {
		value, err := q.MMSValue()
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeQuality(value)
		if err != nil || got != q {
			t.Fatal(q, got, err)
		}
	}
	value, _ := OperatorBlocked.MMSValue()
	bytes, _ := value.Bytes()
	if bytes[0] != 0 || bytes[1] != 8 {
		t.Fatal(bytes)
	}
	if !Test.GoodValidity() || !Test.Has(Test) || Quality(1<<31).Valid() {
		t.Fatal("quality policy conflates validity and flags")
	}
}
func TestCommandUsesDeclaredOrderAndWidths(t *testing.T) {
	spec := controlType()
	spec.Components[0], spec.Components[3] = spec.Components[3], spec.Components[0]
	spec.Components = append(spec.Components, data.Component{Name: "operTm", Type: data.TypeSpec{Kind: data.KindUTC}})
	command, err := BuildCommand(spec, data.Boolean(true), Origin{Category: 2, ID: []byte("station")}, 255, ControlOptions{Time: time.Unix(100, 0), InterlockCheck: true})
	if err != nil {
		t.Fatal(err)
	}
	_, path, _ := data.ResolveComponent(spec, "ctlNum")
	number, _ := data.ValueAt(command, path)
	n, _ := number.Uint()
	if n != 255 {
		t.Fatal(number)
	}
	_, path, _ = data.ResolveComponent(spec, "operTm")
	stamp, _ := data.ValueAt(command, path)
	raw, _ := stamp.Bytes()
	if string(raw) != string(make([]byte, 8)) {
		t.Fatal(raw)
	}
	if _, err = BuildCommand(spec, data.Integer(1), Origin{}, 1, ControlOptions{}); err == nil {
		t.Fatal("wrong ctlVal type accepted")
	}
	spec.Components = append(spec.Components, data.Component{Name: "vendor", Type: data.TypeSpec{Kind: data.KindBoolean}})
	if _, err = BuildCommand(spec, data.Boolean(true), Origin{}, 1, ControlOptions{}); err == nil {
		t.Fatal("unknown Oper component accepted")
	}
}
