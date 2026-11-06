// SPDX-License-Identifier: GPL-3.0-or-later
package pcap

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"
)

func TestEthernetCaptureGolden(t *testing.T) {
	var out bytes.Buffer
	w, err := NewWriter(&out)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.WriteFrame(time.Unix(1700000000, 500000000), []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	want := "d4c3b2a1020004000000000000000000ffff00000100000000f1536520a107000300000003000000010203"
	if hex.EncodeToString(out.Bytes()) != want {
		t.Fatalf("capture %x", out.Bytes())
	}
}
