// SPDX-License-Identifier: GPL-3.0-or-later
package data

import "testing"

func TestComponentPathsPreserveSiblingsAndDeclarations(t *testing.T) {
	element := TypeSpec{Kind: KindInteger, Width: 24}
	spec := TypeSpec{Kind: KindStructure, Components: []Component{{Name: "values", Type: TypeSpec{Kind: KindArray, Length: 2, Element: &element}}, {Name: "enabled", Type: TypeSpec{Kind: KindBoolean}}}}
	typ, path, err := ResolveComponent(spec, "values.1")
	if err != nil || typ.Width != 24 {
		t.Fatal(typ, path, err)
	}
	original := Structure(Array(Integer(7), Integer(8)), Boolean(true))
	updated, err := WithComponent(original, path, Integer(99))
	if err != nil || ValidateValue(spec, updated) != nil {
		t.Fatal(updated, err)
	}
	old, _ := ValueAt(original, path)
	next, _ := ValueAt(updated, path)
	if !old.Equal(Integer(8)) || !next.Equal(Integer(99)) {
		t.Fatal(old, next)
	}
	copy := CloneType(spec)
	copy.Components[0].Type.Element.Width = 8
	if spec.Components[0].Type.Element.Width != 24 {
		t.Fatal("type clone aliases the original")
	}
	for _, bad := range []string{"values.-1", "values.2", "values.", "enabled.child", "missing"} {
		if _, _, err := ResolveComponent(spec, bad); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	if _, err := WithComponent(original, []int{-1}, Boolean(false)); err == nil {
		t.Fatal("negative path accepted")
	}
}
