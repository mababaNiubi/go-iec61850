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
	"github.com/mababaNiubi/go-iec61850/sv"
)

func showSV(e sv.Event) {
	fmt.Printf("%s svID=%s smpCnt=%d confRev=%d sync=%d bytes=%d missing=%d\n", e.Kind, e.ASDU.SVID, e.ASDU.SampleCount, e.ASDU.ConfRev, e.ASDU.SampleSync, len(e.ASDU.Data), e.MissingSamples)
}
func legacySVSubscriber(args []string) error {
	flags := flag.NewFlagSet("sv-subscriber", flag.ContinueOnError)
	iface := flags.String("interface", "", "explicit Ethernet interface name/description/index")
	list := flags.Bool("list", false, "list capture interfaces without sending")
	promiscuous := flags.Bool("promiscuous", false, "request promiscuous capture when the adapter does not deliver SV/GOOSE multicast groups")
	duration := flags.Duration("duration", 5*time.Second, "run duration; 0 waits for Ctrl+C")
	count := flags.Int("count", 0, "stop after N accepted samples; 0 waits for duration")
	vlan := flags.Int("vlan", -2, "VLAN ID filter; -1 requires untagged, -2 accepts both")
	destination := flags.String("destination", "01:0c:cd:04:00:aa", "SV multicast MAC filter")
	appid := flags.String("appid", "0x4000", "SV application identifier filter")
	svid := flags.String("svid", "sampleSV1", "sampled-value stream identifier")
	dataset := flags.String("dataset", "sample/LLN0$sv", "dataset reference filter")
	revision := flags.Uint("confrev", 3, "expected revision; 0 pins the first")
	modulus := flags.Uint("modulus", 65536, "sample counter modulus")
	length := flags.Int("data-length", 64, "expected raw dataset byte count")
	timeout := flags.Duration("timeout", time.Second, "local sample-stream timeout; SV has no TAL")
	allowUnsync := flags.Bool("allow-unsynchronized", true, "accept sync=0 (enabled for software demo)")
	allowRestart := flags.Bool("allow-restart-after-timeout", false, "allow an expired stream to restart at counter zero")
	capture := flags.String("capture", "", "optional incoming PCAP file")
	input := flags.String("input-frame", "", "decode/validate one offline Ethernet frame")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *list {
		return cli.List(os.Stdout)
	}
	if *duration < 0 || *count < 0 || *vlan < -2 || *vlan > 4094 || uint64(*revision) > 0xffffffff || *modulus < 2 || *modulus > 65536 || *length < 0 || *timeout <= 0 {
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
	cfg := sv.SubscriptionConfig{Destination: mac, APPID: uint16(id), SVID: *svid, DataSet: *dataset, ConfRev: uint32(*revision), DataLength: *length, CounterModulus: uint32(*modulus), Timeout: *timeout, AllowUnsynchronized: *allowUnsync, AllowRestartAfterTimeout: *allowRestart}
	if *vlan == -1 {
		cfg.RequireUntagged = true
	} else if *vlan >= 0 {
		value := uint16(*vlan)
		cfg.VLANID = &value
	}
	subscriber, err := sv.NewSubscriber(cfg)
	if err != nil {
		return err
	}
	if *input != "" {
		raw, err := os.ReadFile(*input)
		if err != nil {
			return err
		}
		frame, err := sv.DecodeFrame(raw)
		if err != nil {
			return err
		}
		events, err := subscriber.Process(frame)
		if err != nil {
			return err
		}
		if len(events) == 0 {
			return fmt.Errorf("no matching ASDU")
		}
		for _, e := range events {
			showSV(e)
		}
		return nil
	}
	if *iface == "" {
		return fmt.Errorf("specify -interface; use -list to inspect adapters")
	}
	conn, err := ethernet.Open(ethernet.Config{Interface: *iface, EtherType: sv.EtherType, Promiscuous: *promiscuous})
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
			showSV(event)
			if event.Kind != sv.Expired {
				accepted++
			}
			if *count > 0 && accepted >= *count {
				cancel()
				err = <-result
				return cli.Complete(err, accepted, *count, "samples")
			}
		case err = <-result:
		drain:
			for {
				select {
				case event := <-subscriber.Events():
					showSV(event)
					if event.Kind != sv.Expired {
						accepted++
					}
				default:
					break drain
				}
			}
			fmt.Printf("RECEIVED %d samples\n", accepted)
			return cli.Complete(err, accepted, *count, "samples")
		}
	}
}
