// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/mababaNiubi/go-iec61850/cmd/internal/cli"
	"github.com/mababaNiubi/go-iec61850/ethernet"
	"github.com/mababaNiubi/go-iec61850/goose"
)

func showGoose(e goose.Event) {
	fmt.Printf("%s st=%d sq=%d confRev=%d values=%d tal=%dms\n", e.Kind, e.Frame.PDU.StateNumber, e.Frame.PDU.SequenceNumber, e.Frame.PDU.ConfRev, len(e.Frame.PDU.Values), e.Frame.PDU.TimeAllowedToLive)
}
func legacyGooseSubscriber(args []string) error {
	flags := flag.NewFlagSet("goose-subscriber", flag.ContinueOnError)
	iface := flags.String("interface", "", "explicit Ethernet interface name/description/index")
	list := flags.Bool("list", false, "list capture interfaces without sending")
	promiscuous := flags.Bool("promiscuous", false, "request promiscuous capture when the adapter does not deliver SV/GOOSE multicast groups")
	duration := flags.Duration("duration", 5*time.Second, "run duration; 0 waits for Ctrl+C")
	count := flags.Int("count", 0, "stop after this many accepted frames; 0 waits for duration")
	vlan := flags.Int("vlan", -2, "VLAN ID filter; -1 requires untagged, -2 accepts both")
	destination := flags.String("destination", "01:0c:cd:01:00:aa", "GOOSE multicast MAC filter")
	appid := flags.String("appid", "0x1000", "GOOSE application identifier filter")
	control := flags.String("gocb", "sample/LLN0$GO$interop", "GOOSE control block reference")
	dataset := flags.String("dataset", "sample/LLN0$interop", "GOOSE dataset reference")
	revision := flags.Uint("confrev", 3, "expected configuration revision; 0 pins the first")
	simulation := flags.Bool("allow-simulation", false, "accept simulated dataset values")
	commission := flags.Bool("allow-commissioning", false, "accept datasets marked needs commissioning")
	allowRestart := flags.Bool("allow-restart-after-timeout", false, "allow a timed-out publisher to restart with a new initial event")
	capture := flags.String("capture", "", "optional incoming PCAP file")
	input := flags.String("input-frame", "", "decode/validate one offline Ethernet frame")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *list {
		return cli.List(os.Stdout)
	}
	if *duration < 0 || *count < 0 || *vlan < -2 || *vlan > 4094 || uint64(*revision) > 0xffffffff {
		return fmt.Errorf("invalid subscription limits")
	}
	mac, err := cli.MAC(*destination)
	if err != nil {
		return err
	}
	id, err := strconv.ParseUint(*appid, 0, 16)
	if err != nil {
		return err
	}
	cfg := goose.SubscriptionConfig{Destination: mac, APPID: uint16(id), GoCBRef: *control, DataSet: *dataset, ConfRev: uint32(*revision), AllowSimulation: *simulation, AllowNeedsCommission: *commission, AllowRestartAfterTimeout: *allowRestart}
	if *vlan == -1 {
		cfg.RequireUntagged = true
	} else if *vlan >= 0 {
		value := uint16(*vlan)
		cfg.VLANID = &value
	}
	subscriber, err := goose.NewSubscriber(cfg)
	if err != nil {
		return err
	}
	if *input != "" {
		raw, err := os.ReadFile(*input)
		if err != nil {
			return err
		}
		frame, err := goose.DecodeFrame(raw)
		if err != nil {
			return err
		}
		event, err := subscriber.Process(frame)
		if err != nil {
			return err
		}
		showGoose(event)
		return nil
	}
	if *iface == "" {
		return fmt.Errorf("specify -interface; use -list to inspect adapters")
	}
	conn, err := ethernet.Open(ethernet.Config{Interface: *iface, EtherType: goose.EtherType, Promiscuous: *promiscuous})
	if err != nil {
		return err
	}
	defer conn.Close()
	link, closeCapture, err := cli.Record(conn, *capture)
	if err != nil {
		return err
	}
	defer closeCapture()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- subscriber.Run(ctx, link) }()
	fmt.Printf("READY %s\n", conn.Interface().Name)
	accepted := 0
	for {
		select {
		case event := <-subscriber.Events():
			showGoose(event)
			if event.Kind != goose.Expired {
				accepted++
			}
			if *count > 0 && accepted >= *count {
				cancel()
				err = <-result
				return cli.Complete(err, accepted, *count, "frames")
			}
		case err = <-result:
		drain:
			for {
				select {
				case event := <-subscriber.Events():
					showGoose(event)
					if event.Kind != goose.Expired {
						accepted++
					}
				default:
					break drain
				}
			}
			fmt.Printf("RECEIVED %d frames\n", accepted)
			return cli.Complete(err, accepted, *count, "frames")
		}
	}
}
