// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/mababaNiubi/go-iec61850/cmd/internal/cli"
	"github.com/mababaNiubi/go-iec61850/cmd/internal/models/sv"
	"github.com/mababaNiubi/go-iec61850/ethernet"
	"github.com/mababaNiubi/go-iec61850/sv"
)

func run() error {
	iface := flag.String("interface", "", "explicit Ethernet interface name/description/index")
	list := flag.Bool("list", false, "list capture interfaces without sending")
	duration := flag.Duration("duration", time.Second, "run duration; 0 waits for Ctrl+C")
	interval := flag.Duration("interval", time.Millisecond, "frame interval, independent of SV sample-rate metadata")
	modulus := flag.Uint("modulus", 65536, "per-stream sample counter modulus, e.g.4000")
	vlan := flag.Int("vlan", 123, "VLAN ID; -1 sends untagged Ethernet")
	priority := flag.Int("priority", 4, "VLAN priority 0..7")
	destination := flag.String("destination", "01:0c:cd:04:00:aa", "SV multicast MAC")
	appid := flag.String("appid", "0x4000", "SV application identifier")
	svid := flag.String("svid", "sampleSV1", "sampled-value stream identifier")
	dataset := flag.String("dataset", "sample/LLN0$sv", "dataset reference; empty omits it")
	sync := flag.Uint("sync", 0, "sample synchronization: 0 unsynchronized,1 local,2 global")
	capture := flag.String("capture", "", "optional outgoing PCAP file")
	output := flag.String("output-frame", "", "write one deterministic offline frame; do not open a network interface")
	flag.Parse()
	if *list {
		return cli.List(os.Stdout)
	}
	if *vlan < -1 || *vlan > 4094 || *priority < 0 || *priority > 7 || *duration < 0 || *interval <= 0 || *modulus < 2 || *modulus > 65536 || *sync > 2 {
		return fmt.Errorf("invalid publisher configuration")
	}
	mac, err := cli.MAC(*destination)
	if err != nil {
		return err
	}
	id, err := strconv.ParseUint(*appid, 0, 16)
	if err != nil {
		return err
	}
	frame := svmodel.Frame([6]byte{2, 0xaa, 0xbb, 0xcc, 0xdd, 1})
	frame.Destination = mac
	frame.APPID = uint16(id)
	frame.PDU.ASDUs[0].SVID = *svid
	frame.PDU.ASDUs[0].DataSet = *dataset
	frame.PDU.ASDUs[0].SampleSync = uint8(*sync)
	if *vlan < 0 {
		frame.VLAN = nil
	} else {
		frame.VLAN = &sv.VLAN{Priority: uint8(*priority), ID: uint16(*vlan)}
	}
	if *output != "" {
		encoded, err := sv.EncodeFrame(frame)
		if err != nil {
			return err
		}
		if err = cli.WriteFrameFile(*output, encoded); err != nil {
			return err
		}
		fmt.Printf("WROTE %d Ethernet bytes to %s\n", len(encoded), *output)
		return nil
	}
	if *iface == "" {
		return fmt.Errorf("specify -interface; use -list to inspect adapters")
	}
	conn, err := ethernet.Open(ethernet.Config{Interface: *iface, EtherType: sv.EtherType})
	if err != nil {
		return err
	}
	defer conn.Close()
	frame.Source = conn.MAC()
	link, closeCapture, err := cli.Record(conn, *capture)
	if err != nil {
		return err
	}
	defer closeCapture()
	publisher, err := sv.NewPublisher(link, sv.PublisherConfig{Frame: frame, Interval: *interval, CounterModulus: uint32(*modulus)})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}
	fmt.Printf("READY %s\n", conn.Interface().Name)
	err = publisher.Run(ctx)
	fmt.Printf("SENT %d frames\n", publisher.Sent())
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
