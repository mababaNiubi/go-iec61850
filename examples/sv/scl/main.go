// SPDX-License-Identifier: GPL-3.0-or-later
// Decode model-defined SV fields without hard-coded byte offsets or a NIC.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/scl"
	"github.com/mababaNiubi/go-iec61850/simulator"
	"github.com/mababaNiubi/go-iec61850/sv"
)

func run() error {
	file := flag.String("file", "scl/testdata/sv-lab.scd", "SCD/CID file")
	reference := flag.String("stream", "MUA/AP1/LD0/MSVCB01", "IED/AP/LDevice/SVCB")
	flag.Parse()
	system, err := scl.ParseFile(*file)
	if err != nil {
		return err
	}
	profile, err := system.SVStream(*reference)
	if err != nil {
		return err
	}
	field, err := profile.Layout.Field("TCTR1$MX$Amp$instMag", "i")
	if err != nil {
		return err
	}
	quality, err := profile.Layout.QualityFor(field)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %d bytes per ASDU, %d ASDUs, current offset=%d\n", profile.SVID, profile.Layout.Size, profile.ASDUCount, field.Offset)
	// This software example has no synchronized sampling clock. Real receivers
	// normally keep the default Synchronized policy; the opt-in is explicit.
	config, err := profile.SubscriptionConfig(sv.SubscriptionOptions{SyncPolicy: sv.AnySynchronization})
	if err != nil {
		return err
	}
	subscriber, err := sv.NewSubscriber(config)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	link, err := simulator.NewMemoryLink(4)
	if err != nil {
		return err
	}
	defer link.Close()
	result := make(chan error, 1)
	receiver := &sv.Receiver{}
	remove, err := receiver.Add(subscriber, profile.ValidateFrame, func(events []sv.Event, err error) {
		if err == nil {
			for _, event := range events {
				value, e := field.Decode(event.ASDU.Data)
				if e != nil {
					err = e
					break
				}
				fmt.Printf("smpCnt=%d: raw current=%s\n", event.ASDU.SampleCount, value)
			}
		}
		select {
		case result <- err:
		default:
		}
	})
	if err != nil {
		return err
	}
	defer remove()
	done := make(chan error, 1)
	go func() { done <- receiver.Run(ctx, link) }()
	// Always stop and join the reader before unregistering or closing its link.
	defer func() { cancel(); <-done }()
	frame, err := profile.NewFrame([6]byte{2, 0, 0, 0, 0, 1})
	if err != nil {
		return err
	}
	for i := range frame.PDU.ASDUs {
		asdu := &frame.PDU.ASDUs[i]
		asdu.SampleCount = uint16(i)
		asdu.SampleSync = 0
		if err := field.Encode(asdu.Data, data.Integer(int64(12345+i))); err != nil {
			return err
		}
		if quality != nil {
			if err := quality.Encode(asdu.Data, data.Unsigned(0)); err != nil {
				return err
			}
		}
	}
	raw, err := sv.EncodeFrame(frame)
	if err != nil {
		return err
	}
	if err := link.WriteFrame(ctx, raw); err != nil {
		return err
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
