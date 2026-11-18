// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import "testing"

// BASE-01: the profile matrix is well-formed, versioned and separates the
// four roles. A row may not claim an owning package for an unsupported
// service, because there is nothing to import.
func TestCapabilityMatrixWellFormed(t *testing.T) {
	m := Capabilities()
	if m.Version == "" {
		t.Fatal("capability matrix is not versioned")
	}
	areas := map[string]bool{}
	for _, c := range m.Rows {
		if c.Service == "" {
			t.Fatalf("empty service in area %q", c.Area)
		}
		switch c.Status {
		case Supported, Conditional, Unsupported:
		default:
			t.Fatalf("%s: invalid status %q", c.Service, c.Status)
		}
		if c.Status == Unsupported && c.Package != "" {
			t.Fatalf("%s: unsupported service names package %q", c.Service, c.Package)
		}
		if c.Status == Conditional && c.Restriction == "" {
			t.Fatalf("%s: conditional service without a restriction", c.Service)
		}
		if c.Status != Conditional && c.Restriction != "" {
			t.Fatalf("%s: only conditional services carry restrictions", c.Service)
		}
		areas[c.Area] = true
	}
	for _, want := range []string{"client", "server", "goose", "sv"} {
		if !areas[want] {
			t.Fatalf("matrix is missing the %s role", want)
		}
	}
}
