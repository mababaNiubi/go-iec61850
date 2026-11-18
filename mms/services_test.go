// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"bytes"
	"testing"
)

// The negotiated advertisement stays byte-identical to the reviewed profile:
// status, directory, identify, read, write, attributes, the named-variable-
// list services, informationReport and conclude; no file/journal/cancel. The
// bytes were established by interoperation with an independent peer. If this
// test fails, the service profile changed; review the change against the
// dispatch table before updating the expectation. Mapping individual bits to
// ISO 9506-2 service identifiers requires the licensed text and remains TBD.
func TestAdvertisedServicesMatchReviewedProfile(t *testing.T) {
	want := [11]byte{0xee, 0x1c, 0, 0, 0, 0, 0, 0, 0, 0x01, 0x10}
	got := localParameters(Config{}).Services
	if !bytes.Equal(got[:], want[:]) {
		t.Fatalf("service advertisement changed: got % x, want % x", got, want)
	}
}

// Every advertised confirmed-service opcode must be unique and every service
// must carry either an opcode or a named handler, so a row cannot silently
// drift away from its implementation site.
func TestAdvertisedServicesWellFormed(t *testing.T) {
	services := AdvertisedServices()
	if len(services) == 0 {
		t.Fatal("no advertised services")
	}
	opcodes := map[byte]string{}
	names := map[string]bool{}
	for _, s := range services {
		if s.Name == "" || names[s.Name] {
			t.Fatalf("missing or duplicate service name %q", s.Name)
		}
		names[s.Name] = true
		if s.Opcode == 0 && s.Handled == "" {
			t.Fatalf("%s has neither an opcode nor a handler", s.Name)
		}
		if s.Opcode != 0 {
			if other, ok := opcodes[s.Opcode]; ok {
				t.Fatalf("opcode %#x dispatched for both %s and %s", s.Opcode, other, s.Name)
			}
			opcodes[s.Opcode] = s.Name
		}
	}
}
