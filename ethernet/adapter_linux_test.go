// SPDX-License-Identifier: GPL-3.0-or-later
//go:build linux

package ethernet

import (
	"encoding/binary"
	"testing"
)

func TestRestoreOffloadedVLAN(t *testing.T) {
	frame := testFrame(0, 0x88b8)
	aux := make([]byte, 20)
	binary.NativeEndian.PutUint32(aux, tpStatusVLANValid|tpStatusVLANTPIDValid)
	binary.NativeEndian.PutUint16(aux[16:18], 0xe801)
	binary.NativeEndian.PutUint16(aux[18:20], 0x8100)
	got, err := restoreVLAN(frame, aux)
	if err != nil || len(got) != len(frame)+4 || binary.BigEndian.Uint16(got[12:14]) != 0x8100 || binary.BigEndian.Uint16(got[14:16]) != 0xe801 || !matchesEtherType(got, 0x88b8) {
		t.Fatalf("VLAN reconstruction %x %v", got, err)
	}
	// Preserve DEI1 when the kernel actually supplies it. Older kernels can
	// lose this bit before PACKET_AUXDATA; userspace cannot reconstruct it.
	binary.NativeEndian.PutUint16(aux[16:18], 0xf801)
	got, err = restoreVLAN(frame, aux)
	if err != nil || binary.BigEndian.Uint16(got[14:16]) != 0xf801 {
		t.Fatalf("supplied DEI bit lost: %x %v", got, err)
	}
	// Priority-tagged VLAN zero is signalled by status, not a nonzero TCI.
	binary.NativeEndian.PutUint16(aux[16:18], 0)
	got, err = restoreVLAN(frame, aux)
	if err != nil || len(got) != 64 {
		t.Fatalf("zero TCI lost: %x %v", got, err)
	}
	inner := testFrame(1, 0x88b8)
	got, err = restoreVLAN(inner, aux)
	if err != nil || len(got) != 64 || !matchesEtherType(got, 0x88b8) {
		t.Fatalf("outer tag with inner VLAN: %x %v", got, err)
	}
	if _, err := restoreVLAN(frame, aux[:10]); err == nil {
		t.Fatal("short metadata accepted")
	}
}
