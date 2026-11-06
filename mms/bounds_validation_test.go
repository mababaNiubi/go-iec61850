// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

func TestInitiateDetailBounds(t *testing.T) {
	version := ber.Uint(0x80, 1)
	parameters := ber.Encode(0x81, []byte{5, 0xf1, 0})
	services := ber.Encode(0x82, []byte{3, 0xee, 0x1c, 0, 0, 0, 0, 0, 0, 0, 1, 0x10})
	build := func(tag byte, nesting *uint64, details ...[]byte) []byte {
		fields := [][]byte{ber.Uint(0x80, 65000), ber.Uint(0x81, 5), ber.Uint(0x82, 5)}
		if nesting != nil {
			fields = append(fields, ber.Uint(0x83, *nesting))
		}
		fields = append(fields, ber.Encode(0xa4, details...))
		return ber.Encode(tag, fields...)
	}
	nesting := uint64(16)
	for _, tag := range []byte{0xa8, 0xa9} {
		p, err := decodeInitiate(build(tag, &nesting, version, parameters, services), tag)
		if err != nil || p.NestingLevel != 16 || !p.Supports(4) {
			t.Fatalf("valid Initiate %#x: %+v, %v", tag, p, err)
		}
		p, err = decodeInitiate(build(tag, nil, version, parameters, services), tag)
		if err != nil || p.NestingLevel != 16 {
			t.Fatalf("omitted optional nesting: %+v, %v", p, err)
		}
		bad := [][][]byte{
			{version, version, parameters, services},
			{version, parameters, parameters, services},
			{version, parameters, services, services},
			{version, ber.Encode(0x81, []byte{5, 0xf1, 1}), services},
			{version, ber.Encode(0x81, []byte{4, 0xf1, 0}), services},
			{version, parameters, ber.Encode(0x82, []byte{3, 0xee, 0x1c, 0, 0, 0, 0, 0, 0, 0, 1, 0x11})},
			{version, parameters, ber.Encode(0x82, []byte{2, 0xee, 0x1c, 0, 0, 0, 0, 0, 0, 0, 1, 0x10})},
			{version, parameters},
			{version, parameters, services, ber.Encode(0x83)},
		}
		for i, details := range bad {
			if _, err := decodeInitiate(build(tag, &nesting, details...), tag); err == nil {
				t.Fatalf("accepted malformed Initiate detail %d, tag %#x", i, tag)
			}
		}
		for _, limit := range []uint64{0, 65, 128, 0xffffffff} {
			if _, err := decodeInitiate(build(tag, &limit, version, parameters, services), tag); err == nil {
				t.Fatalf("accepted unsupported nesting limit %d", limit)
			}
		}
	}
}

func TestParameterCBBIntersection(t *testing.T) {
	cfg, _ := (Config{}).normalized()
	local := localParameters(cfg)
	peer := local
	peer.ParameterCBB = [2]byte{0x81, 0x80}
	negotiated := negotiate(local, peer)
	if negotiated.ParameterCBB != ([2]byte{0x81, 0}) {
		t.Fatalf("negotiated unsupported parameters: %x", negotiated.ParameterCBB)
	}
	decoded, err := decodeInitiate(encodeInitiate(0xa9, negotiated), 0xa9)
	if err != nil || decoded.ParameterCBB != negotiated.ParameterCBB {
		t.Fatalf("parameter negotiation roundtrip: %x %v", decoded.ParameterCBB, err)
	}
}

func TestModelNamesUniqueAndAddressable(t *testing.T) {
	model := NewModel()
	structure := Variable{Name: DomainName("test", "a"), Type: TypeSpec{Kind: KindStructure, Components: []Component{{Name: "b", Type: TypeSpec{Kind: KindBoolean}}, {Type: TypeSpec{Kind: KindBoolean}}}}, Value: Structure(Boolean(true), Boolean(false))}
	if err := model.Add(structure); err != nil {
		t.Fatal(err)
	}
	if err := model.Add(Variable{Name: DomainName("test", "a$b"), Type: TypeSpec{Kind: KindBoolean}, Value: Boolean(false)}); err != nil {
		t.Fatal(err)
	}
	names, err := model.Names(context.Background(), DomainScope, "test")
	if err != nil || !reflect.DeepEqual(names, []string{"a", "a$b"}) {
		t.Fatalf("directory contains duplicates or unnamed members: %v, %v", names, err)
	}
	for _, name := range names {
		if _, err := model.Read(context.Background(), Access(DomainName("test", name))); err != nil {
			t.Fatalf("directory exposed inaccessible member %q: %v", name, err)
		}
	}
}

func TestComponentIdentifierBoundsAndTypeDepth(t *testing.T) {
	valid := TypeSpec{Kind: KindStructure, Components: []Component{{Name: strings.Repeat("x", 64), Type: TypeSpec{Kind: KindBoolean}}}}
	if _, err := EncodeType(valid); err != nil {
		t.Fatalf("rejected 64-octet component: %v", err)
	}
	invalid := valid
	invalid.Components = []Component{{Name: strings.Repeat("x", 65), Type: TypeSpec{Kind: KindBoolean}}}
	if _, err := EncodeType(invalid); err == nil {
		t.Fatal("encoded oversized component identifier")
	}
	encoded := ber.Encode(0xa2, ber.Encode(0xa1, ber.Encode(0x30, ber.Encode(0x80, []byte(strings.Repeat("x", 65))), ber.Encode(0xa1, ber.Encode(0x83)))))
	if _, err := DecodeType(encoded); err == nil {
		t.Fatal("decoded oversized component identifier")
	}
	emptyName := ber.Encode(0xa2, ber.Encode(0xa1, ber.Encode(0x30, ber.Encode(0x80), ber.Encode(0xa1, ber.Encode(0x83)))))
	if _, err := DecodeType(emptyName); err == nil {
		t.Fatal("decoded explicitly empty component identifier")
	}
	boolean := TypeSpec{Kind: KindBoolean}
	array := TypeSpec{Kind: KindArray, Length: 1, Element: &boolean}
	structure := TypeSpec{Kind: KindStructure, Components: []Component{{Name: "items", Type: array}}}
	if TypeDepth(boolean) != 1 || TypeDepth(array) != 2 || TypeDepth(structure) != 3 {
		t.Fatal("incorrect TypeSpecification nesting depth")
	}
	cycle := TypeSpec{Kind: KindArray, Length: 1}
	cycle.Element = &cycle
	if TypeDepth(cycle) != ber.MaxDepth+1 || TypeDepth(TypeSpec{}) != ber.MaxDepth+1 {
		t.Fatal("invalid/cyclic type did not exceed supported nesting")
	}
}
