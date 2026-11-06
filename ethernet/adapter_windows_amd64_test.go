// SPDX-License-Identifier: GPL-3.0-or-later
//go:build windows && amd64

package ethernet

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestWindowsABI(t *testing.T) {
	if unsafe.Sizeof(pcapHeader{}) != 16 || unsafe.Sizeof(pcapInterface{}) != 40 || unsafe.Sizeof(bpfProgram{}) != 16 {
		t.Fatal("Npcap ABI layout mismatch")
	}
}

// This opt-in test opens and closes the selected adapter without transmitting
// or retaining any captured packets. It only verifies access/link type/cancel.
func TestInstalledNpcapReadOnly(t *testing.T) {
	name := os.Getenv("GOOSE_ETHERNET_READONLY_INTERFACE")
	if name == "" {
		t.Skip("set GOOSE_ETHERNET_READONLY_INTERFACE for installed-adapter read-only check")
	}
	interfaces, err := Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range interfaces {
		t.Logf("%s | %s | MAC=%x flags=%#x", i.Name, i.Description, i.MAC, i.Flags)
		if name == "auto-vmnet1" && strings.Contains(i.Description, "VMnet1") {
			name = i.Name
		}
	}
	c, err := Open(Config{Interface: name, EtherType: 0x88b8})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	t.Logf("opened %s | %s | MAC=%x", c.Interface().Name, c.Interface().Description, c.MAC())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	_, err = c.ReadFrame(ctx)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestWindowsLoopbackRejected(t *testing.T) {
	if os.Getenv("GOOSE_ETHERNET_READONLY_INTERFACE") == "" {
		t.Skip("installed adapter check is opt-in")
	}
	interfaces, err := Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range interfaces {
		if strings.Contains(i.Name, "Loopback") {
			c, err := Open(Config{Interface: i.Name, EtherType: 0x88b8})
			if c != nil {
				c.Close()
			}
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("loopback accepted: %v", err)
			}
			return
		}
	}
	t.Skip("Npcap loopback is not enumerated")
}
