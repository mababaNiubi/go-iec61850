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
	"github.com/mababaNiubi/go-iec61850/cmd/internal/models/goose"
	"github.com/mababaNiubi/go-iec61850/ethernet"
	"github.com/mababaNiubi/go-iec61850/goose"
)

func legacyGoosePublisher(args []string) error {
	flags := flag.NewFlagSet("goose-publisher", flag.ContinueOnError)
	iface := flags.String("interface", "", "explicit Ethernet interface name/description/index")
	list := flags.Bool("list", false, "list capture interfaces without sending")
	duration := flags.Duration("duration", 5*time.Second, "run duration; 0 waits for Ctrl+C")
	minimum := flags.Duration("min", 4*time.Millisecond, "initial retransmission interval")
	maximum := flags.Duration("max", time.Second, "stable-state retransmission interval")
	vlan := flags.Int("vlan", 123, "VLAN ID; -1 sends untagged Ethernet")
	priority := flags.Int("priority", 4, "VLAN priority 0..7")
	destination := flags.String("destination", "01:0c:cd:01:00:aa", "GOOSE multicast MAC")
	appid := flags.String("appid", "0x1000", "GOOSE application identifier")
	capture := flags.String("capture", "", "optional outgoing PCAP file")
	output := flags.String("output-frame", "", "write one deterministic offline frame; do not open a network interface")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *list {
		return cli.List(os.Stdout)
	}
	if *vlan < -1 || *vlan > 4094 || *priority < 0 || *priority > 7 || *duration < 0 {
		return fmt.Errorf("invalid VLAN/priority/duration")
	}
	mac, err := cli.MAC(*destination)
	if err != nil {
		return err
	}
	id, err := strconv.ParseUint(*appid, 0, 16)
	if err != nil {
		return err
	}
	frame := goosemodel.Frame([6]byte{2, 0xaa, 0xbb, 0xcc, 0xdd, 1})
	frame.Destination = mac
	frame.APPID = uint16(id)
	if *vlan < 0 {
		frame.VLAN = nil
	} else {
		frame.VLAN = &goose.VLAN{Priority: uint8(*priority), ID: uint16(*vlan)}
	}
	if *output != "" {
		encoded, err := goose.EncodeFrame(frame)
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
	conn, err := ethernet.Open(ethernet.Config{Interface: *iface, EtherType: goose.EtherType})
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
	publisher, err := goose.NewPublisher(link, goose.PublisherConfig{Frame: frame, MinTime: *minimum, MaxTime: *maximum})
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
