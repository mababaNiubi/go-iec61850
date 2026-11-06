// SPDX-License-Identifier: GPL-3.0-or-later
package data

import (
	"testing"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

func TestValueDepth(t *testing.T) {
	if Boolean(true).Depth() != 1 || Array().Depth() != 1 || Structure().Depth() != 1 {
		t.Fatal("scalars and empty composites must have depth one")
	}
	value := Structure(Boolean(true), Array(Integer(1)))
	if value.Depth() != 3 {
		t.Fatalf("nested depth=%d, want 3", value.Depth())
	}
	value = Integer(1)
	for i := 1; i < ber.MaxDepth; i++ {
		value = Array(value)
	}
	if value.Depth() != ber.MaxDepth {
		t.Fatalf("maximum legal depth=%d", value.Depth())
	}
	if Array(value).Depth() != ber.MaxDepth+1 {
		t.Fatal("overly deep value did not exceed the supported limit")
	}
}

func TestValueDepthNodeBudget(t *testing.T) {
	children := make([]Value, ber.MaxElements)
	for i := range children {
		children[i] = Boolean(false)
	}
	if Array(children[:ber.MaxElements-1]...).Depth() != 2 {
		t.Fatal("node budget rejected the maximum legal tree")
	}
	if Array(children...).Depth() != ber.MaxDepth+1 {
		t.Fatal("node budget did not reject an excessively wide tree")
	}
}
