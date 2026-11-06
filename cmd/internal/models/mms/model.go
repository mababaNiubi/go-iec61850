// SPDX-License-Identifier: GPL-3.0-or-later
// Package mmsmodel provides a deterministic sample MMS device for demos.
package mmsmodel

import (
	"fmt"
	mms "github.com/mababaNiubi/go-iec61850/mms"
)

func New() (*mms.Model, error) {
	m := mms.NewModel()
	integer := mms.TypeSpec{Kind: mms.KindInteger, Width: 32}
	boolean := mms.TypeSpec{Kind: mms.KindBoolean}
	variables := []mms.Variable{
		{Name: mms.DomainName("sample", "bool"), Type: boolean, Value: mms.Boolean(true), Writable: true},
		{Name: mms.DomainName("sample", "int"), Type: integer, Value: mms.Integer(42), Writable: true},
		{Name: mms.DomainName("sample", "uint"), Type: mms.TypeSpec{Kind: mms.KindUnsigned, Width: 32}, Value: mms.Unsigned(123), Writable: true},
		{Name: mms.DomainName("sample", "float"), Type: mms.TypeSpec{Kind: mms.KindFloat, Width: 32, ExponentWidth: 8}, Value: mms.Float32(1.25), Writable: true},
		{Name: mms.DomainName("sample", "text"), Type: mms.TypeSpec{Kind: mms.KindVisibleString, Length: -64}, Value: mms.VisibleString("hello"), Writable: true},
		{Name: mms.DomainName("sample", "array"), Type: mms.TypeSpec{Kind: mms.KindArray, Length: 3, Element: &integer}, Value: mms.Array(mms.Integer(1), mms.Integer(2), mms.Integer(3)), Writable: true},
		{Name: mms.DomainName("sample", "struct"), Type: mms.TypeSpec{Kind: mms.KindStructure, Components: []mms.Component{{Name: "flag", Type: boolean}, {Name: "count", Type: integer}}}, Value: mms.Structure(mms.Boolean(true), mms.Integer(7)), Writable: true},
		{Name: mms.DomainName("sample", "readonly"), Type: integer, Value: mms.Integer(9)},
	}
	for i := 0; i < 40; i++ {
		variables = append(variables, mms.Variable{Name: mms.DomainName("sample", fmt.Sprintf("item%03d", i)), Type: integer, Value: mms.Integer(int64(i)), Writable: true})
	}
	for _, variable := range variables {
		if err := m.Add(variable); err != nil {
			return nil, err
		}
	}
	return m, nil
}
