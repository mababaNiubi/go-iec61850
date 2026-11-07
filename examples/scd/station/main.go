// SPDX-License-Identifier: GPL-3.0-or-later
// One engineering file connects a software merging unit, bay IED and HMI.
// All protocol traffic uses encoded Ethernet frames or actual MMS/TCP.
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/goose"
	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
	"github.com/mababaNiubi/go-iec61850/simulator"
	"github.com/mababaNiubi/go-iec61850/sv"
)

func run() error {
	file := flag.String("file", "examples/scd/station/station.scd", "shared SCD engineering model")
	flag.Parse()
	system, err := scl.ParseFile(*file)
	if err != nil {
		return err
	}
	bay, err := system.Device("BayIED", "S1")
	if err != nil {
		return err
	}
	mu, err := system.Device("MUA", "AP1")
	if err != nil {
		return err
	}
	firstInput, err := system.SVInput("BayIED", "S1", "I1")
	if err != nil {
		return err
	}
	profile := firstInput.Profile
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bus := &simulator.MemoryBus{}
	defer bus.Close()
	samples, err := bus.Open(4096)
	if err != nil {
		return err
	}
	defer samples.Close()
	positions, err := bus.Open(4096)
	if err != nil {
		return err
	}
	defer positions.Close()
	input, err := bay.Input("StationHMI", "S1", "breakerPosition")
	if err != nil {
		return err
	}
	status, err := bay.Signal("CTRL", "XCBR1", "ST", "Pos", "stVal")
	if err != nil {
		return err
	}
	power, err := bay.Signal("CTRL", "MMXU1", "MX", "TotW", "mag.f")
	if err != nil {
		return err
	}
	oper, err := bay.Lookup("CTRL", "XCBR1", "CO", "Pos.Oper")
	if err != nil {
		return err
	}
	if err = bay.OnOperate(oper, func(ctx context.Context, c scl.Command) error {
		// Replace this actuator with your hardware driver and publish feedback
		// only when independently observed. This example is a virtual breaker.
		if c.Test || c.SynchroCheck {
			return mms.ObjectAccessUnsupported
		}
		position := scl.Open
		if c.Value {
			position = scl.Closed
		}
		value, _ := position.MMSValue()
		q, _ := ied.Quality(0).MMSValue()
		return bay.Update(ctx, status.Sample(value, q, data.UTC(time.Now()))...)
	}); err != nil {
		return err
	}
	var currents, voltages []sv.Field
	var qualities []sv.Field
	for phase := 1; phase <= 3; phase++ {
		i, e := system.SVInput("BayIED", "S1", fmt.Sprintf("I%d", phase))
		if e != nil {
			return e
		}
		u, e := system.SVInput("BayIED", "S1", fmt.Sprintf("U%d", phase))
		if e != nil {
			return e
		}
		currents = append(currents, i.Field)
		voltages = append(voltages, u.Field)
		if i.Quality != nil {
			qualities = append(qualities, *i.Quality)
		}
		if u.Quality != nil {
			qualities = append(qualities, *u.Quality)
		}
	}
	config, err := profile.SubscriptionConfig(sv.SubscriptionOptions{SyncPolicy: sv.AnySynchronization})
	if err != nil {
		return err
	}
	subscriber, err := sv.NewSubscriber(config)
	if err != nil {
		return err
	}
	ready := make(chan struct{}, 1)
	failures := make(chan error, 1)
	receiver := &sv.Receiver{}
	var total float64
	count := 0
	removeSV, err := receiver.Add(subscriber, profile.ValidateFrame, func(events []sv.Event, err error) {
		if err != nil {
			select {
			case failures <- err:
			default:
			}
			return
		}
		for _, event := range events {
			usable := event.Kind == sv.SampleReceived
			for _, field := range qualities {
				value, err := field.Decode(event.ASDU.Data)
				q, e := value.Uint()
				if err != nil || e != nil || q != 0 {
					usable = false
				}
			}
			if !usable {
				total = 0
				count = 0
				q, _ := ied.Quality(ied.OldData).MMSValue()
				if err := bay.Update(ctx, mms.Assignment{Name: power.Quality, Value: q}); err != nil {
					select {
					case failures <- err:
					default:
					}
				}
				continue
			}
			for phase := range currents {
				i, _ := currents[phase].Decode(event.ASDU.Data)
				u, _ := voltages[phase].Decode(event.ASDU.Data)
				iv, _ := i.Int()
				uv, _ := u.Int()
				// This fixture declares 1 mA / 10 mV sample units. Three phases
				// of a resistive load form a simple power-calculation example.
				total += float64(iv) * 0.001 * float64(uv) * 0.01
			}
			count++
			if count == 80 {
				q, _ := ied.Quality(0).MMSValue()
				if err := bay.Update(ctx, power.Sample(data.Float32(float32(total/80)), q, data.UTC(time.Now()))...); err != nil {
					select {
					case failures <- err:
					default:
					}
				}
				total = 0
				count = 0
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		}
	})
	if err != nil {
		return err
	}
	defer removeSV()
	gooseSubscriber, err := input.Stream.NewSubscriber()
	if err != nil {
		return err
	}
	gooseReceiver := &goose.Receiver{}
	closed := make(chan struct{}, 1)
	removeGOOSE, err := gooseReceiver.Add(gooseSubscriber, func(event goose.Event, err error) {
		if err != nil {
			return
		}
		value, err := input.Value(event)
		if err != nil {
			return
		}
		position, err := scl.ReadPosition(value)
		if err == nil && position == scl.Closed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	})
	if err != nil {
		return err
	}
	defer removeGOOSE()
	readers := make(chan error, 2)
	go func() { readers <- receiver.Run(ctx, samples) }()
	go func() { readers <- gooseReceiver.Run(ctx, positions) }()
	defer func() { cancel(); <-readers; <-readers }()
	bayRuntime, err := simulator.Start(ctx, bay, simulator.Options{Listen: "127.0.0.1:0", Bus: bus})
	if err != nil {
		return err
	}
	defer bayRuntime.Close()
	muRuntime, err := simulator.Start(ctx, mu, simulator.Options{Listen: "127.0.0.1:0", Bus: bus, GOOSE: simulator.GOOSEOff, SV: []simulator.SampledValues{{Profile: profile, Sample: func(ctx context.Context, counter uint16, raw []byte) error {
		for phase := range currents {
			angle := 2 * math.Pi * (float64(counter%80)/80 - float64(phase)/3)
			if err := currents[phase].Encode(raw, data.Integer(int64(10000*math.Sqrt2*math.Sin(angle)))); err != nil {
				return err
			}
			if err := voltages[phase].Encode(raw, data.Integer(int64(23000*math.Sqrt2*math.Sin(angle)))); err != nil {
				return err
			}
		}
		return nil
	}}}})
	if err != nil {
		return err
	}
	defer muRuntime.Close()
	client, err := ied.Dial(ctx, bayRuntime.Address(), ied.Config{})
	if err != nil {
		return err
	}
	defer client.Close()
	select {
	case <-ready:
	case err := <-failures:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
	value, err := client.Read(ctx, power.Value)
	if err != nil {
		return err
	}
	fmt.Println("MU -> SV -> bay calculation -> MMS total power:", value)
	sub, err := client.SubscribeURCB(ctx, mms.DomainName("BayIEDCTRL", "LLN0$RP$events01"), ied.ReportOptions{})
	if err != nil {
		return err
	}
	defer sub.Close()
	if _, err = client.Operate(ctx, "BayIEDCTRL/XCBR1.Pos", data.Boolean(true), ied.ControlOptions{}); err != nil {
		return err
	}
	select {
	case event := <-sub.Events():
		if event.Err != nil {
			return event.Err
		}
		fmt.Println("MMS control -> position report:", len(event.Report.Entries), "member(s)")
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-closed:
		fmt.Println("Position -> GOOSE -> engineered StationHMI input: closed")
	case err := <-failures:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
