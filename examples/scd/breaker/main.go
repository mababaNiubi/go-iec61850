// SPDX-License-Identifier: GPL-3.0-or-later
// The default run includes both endpoints. MMS crosses a loopback TCP socket.
// GOOSE crosses an in-memory Ethernet link with the same frame codecs as a NIC.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/mababaNiubi/go-iec61850/ethernet"
	"github.com/mababaNiubi/go-iec61850/examples/internal/memory"
	"github.com/mababaNiubi/go-iec61850/goose"
	samplemodel "github.com/mababaNiubi/go-iec61850/internal/sample"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
func run() (err error) {
	file := flag.String("file", "", "SCD file (default: embedded bay.scd)")
	role := flag.String("role", "demo", "demo, ied or monitor")
	address := flag.String("address", "", "MMS listen/dial address (demo: ephemeral loopback; other roles: SCD ConnectedAP)")
	iface := flag.String("interface", "", "Ethernet interface for separate IED/monitor roles")
	promisc := flag.Bool("promiscuous", false, "enable promiscuous capture on the specified interface")
	command := flag.String("operate", "", "monitor only: explicitly send open or close once")
	iedName := flag.String("ied", "BayIED", "source IED name")
	ap := flag.String("ap", "S1", "source access point")
	ld := flag.String("ld", "CTRL", "LDevice instance for the application signals")
	flag.Parse()
	if *role != "demo" && *role != "ied" && *role != "monitor" {
		return fmt.Errorf("unknown role %q", *role)
	}
	if *command != "" && (*role != "monitor" || *command != "open" && *command != "close") {
		return fmt.Errorf("-operate open|close requires -role monitor")
	}
	if *role == "demo" && (*iface != "" || *address != "") {
		return fmt.Errorf("demo uses its own loopback and memory links; select ied or monitor for an external address")
	}
	var sys *scl.System
	if *file == "" {
		sys, err = scl.Parse(bytes.NewReader(samplemodel.SCL()))
	} else {
		sys, err = scl.ParseFile(*file)
	}
	if err != nil {
		return err
	}
	d, err := sys.Device(*iedName, *ap)
	if err != nil {
		return err
	}
	points, err := bindPoints(d, *ld)
	if err != nil {
		return err
	}

	// The SCD describes the destination and VLAN. The host supplies a physical NIC.
	var link goose.FrameIO
	var mac [6]byte
	if *role == "demo" {
		link = memory.NewLink()
		mac = [6]byte{2, 0, 0, 0, 0, 1}
	} else if *iface != "" {
		conn, err := ethernet.Open(ethernet.Config{Interface: *iface, EtherType: goose.EtherType, Promiscuous: *promisc})
		if err != nil {
			return err
		}
		link = conn
		mac = conn.MAC()
	}
	if link != nil {
		defer link.Close()
	}
	parent, interrupt := signal.NotifyContext(context.Background(), os.Interrupt)
	defer interrupt()
	if *role == "demo" {
		var cancel context.CancelFunc
		parent, cancel = context.WithTimeout(parent, 10*time.Second)
		defer cancel()
	}
	ctx, stop := context.WithCancelCause(parent)
	var wg sync.WaitGroup
	start := func(work func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := work(); e != nil {
				stop(e)
			}
		}()
	}
	defer func() {
		cause := context.Cause(ctx)
		stop(context.Canceled)
		wg.Wait()
		if cause != nil && (err == nil || errors.Is(err, context.Canceled)) {
			err = cause
		}
	}()
	addr := *address
	if addr == "" {
		addr = d.MMSAddress()
	}
	driver := newSimulator()
	b := bay{device: d, points: points, io: driver}
	if *role != "monitor" {
		if *role == "demo" {
			addr = "127.0.0.1:0"
			driver.blockClose(true)
		}
		if err := b.acquire(ctx); err != nil {
			return err
		}
		if err := d.OnOperate(points.operate, b.perform); err != nil {
			return err
		}
		srv, err := d.NewServer(mms.Config{})
		if err != nil {
			return err
		}
		if addr == "" {
			return fmt.Errorf("supply -address or configure the SCD MMS address")
		}
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		addr = listener.Addr().String()
		start(func() error { return serve(ctx, srv, listener) })
		if link != nil {
			g, err := d.Stream(*ld, "gcbPos")
			if err != nil {
				return err
			}
			start(func() error { return g.Run(ctx, link, mac) })
		}
		if *role == "ied" {
			fmt.Println("MMS server", addr)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return context.Cause(ctx)
				case <-ticker.C:
					if err := b.acquire(ctx); err != nil {
						return err
					}
				}
			}
		}
	}
	// The monitor obtains the subscription from its own ExtRef.intAddr. Its
	// measurements, commands and status confirmation always go over MMS.
	var input scl.Input
	var sub *goose.Subscriber
	if link != nil {
		input, err = d.Input("StationHMI", "S1", "breakerPosition")
		if err != nil {
			return err
		}
		sub, err = input.Stream.NewSubscriber()
		if err != nil {
			return err
		}
		start(func() error { return sub.Run(ctx, link) })
	}
	if addr == "" {
		return fmt.Errorf("supply -address or configure the SCD MMS address")
	}
	client, err := mms.Dial(ctx, addr, d.ClientConfig())
	if err != nil {
		return err
	}
	defer client.Close()
	if *role == "demo" {
		if err := demonstrate(ctx, client, points, sub, input, &b, driver); err != nil {
			return err
		}
		return client.Conclude(ctx)
	}
	return monitor(ctx, client, points, sub, input, *command)
}

func serve(ctx context.Context, s *mms.Server, l net.Listener) error {
	// Also close the listener if cancellation wins the race with Serve starting.
	stop := context.AfterFunc(ctx, func() { s.Close(); l.Close() })
	defer stop()
	defer l.Close()
	err := s.Serve(l)
	cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e := s.Shutdown(cleanup); e != nil {
		return e
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func demonstrate(ctx context.Context, c *mms.Client, p bayPoints, sub *goose.Subscriber, input scl.Input, b *bay, driver *simulator) error {
	if err := waitPosition(ctx, sub, input, scl.Open); err != nil {
		return err
	}
	pos, err := readProcess(ctx, c, p)
	if err != nil {
		return err
	}
	if pos != scl.Open {
		return fmt.Errorf("expected initial open feedback, got %s", pos)
	}
	// First control is denied by the simulated close interlock. No feedback is fabricated.
	if err := operate(ctx, c, p.operate, true, 1); !errors.Is(err, mms.ObjectAccessDenied) {
		return fmt.Errorf("expected close interlock, got %v", err)
	}
	fmt.Println("MMS close blocked by interlock")
	driver.blockClose(false)
	if err := b.acquire(ctx); err != nil {
		return err
	}
	for i, want := range []scl.Position{scl.Closed, scl.Open} {
		if err := operate(ctx, c, p.operate, want == scl.Closed, uint8(i+2)); err != nil {
			return err
		}
		pos, err := readProcess(ctx, c, p)
		if err != nil {
			return err
		}
		if pos != want {
			return fmt.Errorf("MMS feedback %s, expected %s", pos, want)
		}
		if err = waitPosition(ctx, sub, input, want); err != nil {
			return err
		}
	}
	return nil
}
