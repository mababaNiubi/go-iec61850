// SPDX-License-Identifier: GPL-3.0-or-later
//go:build linux

package ethernet

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"
)

// These tests send only after the caller explicitly names an isolated Ethernet
// interface. A separate veth peer exercises the kernel's incoming path; a single
// interface instead exercises AF_PACKET delivery of another socket's outgoing
// traffic. Neither mode configures interface addresses or network routes.
func linuxLiveInterfaces(t *testing.T) (string, string) {
	t.Helper()
	local := os.Getenv("ETHERNET_LIVE_INTERFACE")
	if local == "" {
		t.Skip("set ETHERNET_LIVE_INTERFACE to an explicit isolated Ethernet interface for Linux AF_PACKET tests")
	}
	peer := os.Getenv("ETHERNET_LIVE_PEER_INTERFACE")
	if peer == "" {
		peer = local
	}
	return local, peer
}

func openLinuxLive(t *testing.T, name string, kind uint16, limit int) *Conn {
	t.Helper()
	c, err := Open(Config{Interface: name, EtherType: kind, MaxFrameSize: limit, ReadTimeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func linuxLiveDirections(t *testing.T) [][2]string {
	t.Helper()
	local, peer := linuxLiveInterfaces(t)
	directions := [][2]string{{local, peer}}
	if local != peer {
		directions = append(directions, [2]string{peer, local})
	}
	return directions
}

func exchangeLinuxFrame(t *testing.T, direction [2]string, kind uint16, tagged bool, tci uint16) ([]byte, []byte) {
	t.Helper()
	rx := openLinuxLive(t, direction[1], kind, 1518)
	tx := openLinuxLive(t, direction[0], kind, 1518)
	tags := 0
	if tagged {
		tags = 1
	}
	frame := testFrame(tags, kind)
	copy(frame[:6], []byte{1, 0x0c, 0xcd, 4, 0, 0xaa})
	source := tx.MAC()
	copy(frame[6:12], source[:])
	if tagged {
		binary.BigEndian.PutUint16(frame[14:16], tci)
	}
	marker := []byte(fmt.Sprintf("61850-afpacket-%x", time.Now().UnixNano()))
	copy(frame[len(frame)-len(marker):], marker)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tx.WriteFrame(ctx, frame); err != nil {
		t.Fatal(err)
	}
	for {
		got, err := rx.ReadFrame(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.HasSuffix(got, marker) {
			return got, frame
		}
	}
}

func TestLinuxLiveFrameTransport(t *testing.T) {
	for _, direction := range linuxLiveDirections(t) {
		for _, kind := range []uint16{0x88b8, 0x88ba} {
			for _, tagged := range []bool{false, true} {
				name := fmt.Sprintf("%s->%s/type=%04x/vlan=%v", direction[0], direction[1], kind, tagged)
				t.Run(name, func(t *testing.T) {
					// Keep exact-byte transport checks with PCP6, VID2345, DEI0.
					// DEI1 is a separate capability test, not silently masked here.
					got, frame := exchangeLinuxFrame(t, direction, kind, tagged, 0xc929)
					if !bytes.Equal(got, frame) {
						t.Fatalf("AF_PACKET changed frame bytes:\n got %x\nwant %x", got, frame)
					}
					t.Logf("%s -> %s, %d bytes, EtherType=%#x, tagged=%v", direction[0], direction[1], len(got), kind, tagged)
				})
			}
		}
	}
}

func TestLinuxLiveVLANDEICapability(t *testing.T) {
	for _, direction := range linuxLiveDirections(t) {
		for _, kind := range []uint16{0x88b8, 0x88ba} {
			name := fmt.Sprintf("%s->%s/type=%04x", direction[0], direction[1], kind)
			t.Run(name, func(t *testing.T) {
				// First prove exact fidelity with DEI0 on this same socket path.
				control, controlFrame := exchangeLinuxFrame(t, direction, kind, true, 0xc929)
				if !bytes.Equal(control, controlFrame) {
					t.Fatalf("DEI0 control changed bytes:\n got %x\nwant %x", control, controlFrame)
				}
				got, frame := exchangeLinuxFrame(t, direction, kind, true, 0xd929)
				if bytes.Equal(got, frame) {
					t.Log("DEI1 preserved exactly: sent/received TCI=0xd929")
					return
				}
				withoutDEI := bytes.Clone(frame)
				withoutDEI[14] &^= 0x10
				if bytes.Equal(got, withoutDEI) && os.Getenv("ETHERNET_LIVE_ALLOW_DEI_LOSS") == "1" {
					// Linux4.19's skb_vlan_tag_get masks VLAN_TAG_PRESENT (0x1000)
					// before filling PACKET_AUXDATA, losing the wire's DEI bit.
					// Source: v4.19/include/linux/if_vlan.h and net/packet/af_packet.c.
					// This explicit opt-in reports a limitation; it is not a fidelity
					// pass, and arbitrary changes still fail on every kernel.
					t.Skipf("DEI1 capability unavailable on this kernel/driver path: sent TCI=0xd929, received TCI=0xc929, all other bytes exact; explicitly allowed by ETHERNET_LIVE_ALLOW_DEI_LOSS=1\n sent %x\n received %x", frame, got)
				}
				t.Fatalf("DEI1 fidelity failed (default is strict; explicit ETHERNET_LIVE_ALLOW_DEI_LOSS=1 only permits reporting exact DEI1->0 loss):\n got %x\nwant %x", got, frame)
			})
		}
	}
}

func TestLinuxLiveReadBounds(t *testing.T) {
	local, peer := linuxLiveInterfaces(t)
	rx := openLinuxLive(t, peer, 0x88ba, 60)
	tx := openLinuxLive(t, local, 0x88ba, 1518)
	frame := make([]byte, 128)
	copy(frame, testFrame(0, 0x88ba))
	copy(frame[:6], []byte{1, 0x0c, 0xcd, 4, 0, 0xaa})
	source := tx.MAC()
	copy(frame[6:12], source[:])
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tx.WriteFrame(ctx, frame); err != nil {
		t.Fatal(err)
	}
	if _, err := rx.ReadFrame(ctx); !errors.Is(err, ErrFrameSize) {
		t.Fatalf("oversized native receive: %v", err)
	}
}

func TestLinuxLiveCancelAndClose(t *testing.T) {
	local, _ := linuxLiveInterfaces(t)
	// An experimental EtherType avoids unrelated MMS/GOOSE/SV/IP traffic while
	// exercising cancellation of the native nonblocking receive loop.
	rx := openLinuxLive(t, local, 0x88b5, 1518)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := rx.ReadFrame(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("native receive timeout: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := rx.ReadFrame(context.Background()); done <- err }()
	if err := rx.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("native close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing AF_PACKET did not release the reader")
	}
}

func TestLinuxRejectsLoopback(t *testing.T) {
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback == 0 {
			continue
		}
		conn, err := Open(Config{Interface: iface.Name})
		if conn != nil {
			conn.Close()
		}
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("loopback %s was not rejected before opening a raw socket: %v", iface.Name, err)
		}
		return
	}
	t.Skip("no loopback interface exists in this network namespace")
}
