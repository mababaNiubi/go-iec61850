// SPDX-License-Identifier: GPL-3.0-or-later
// Run from the repository root: go run ./examples/scd/station
//
// One SCD connects the MU process port, the bay's ServerAt process port and
// its station-bus port. Replace the sample source and callbacks to integrate
// real acquisition and actuators; the public SDK owns the protocol lifecycle.
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
	"github.com/mababaNiubi/go-iec61850/simulator"
	"github.com/mababaNiubi/go-iec61850/sv"
)

func run() error {
	file := flag.String("file", "examples/scd/station/station.scd", "SCD engineering model")
	flag.Parse()
	system, err := scl.ParseFile(*file)
	if err != nil {
		return err
	}
	bay, err := system.Device("BayIED", "S1")
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
	// Production code calls its actuator here and updates ST only after
	// independently observing equipment feedback. This is a virtual breaker.
	if err = bay.OnControl(oper, func(ctx context.Context, command scl.Command) error {
		if command.Test || command.SynchroCheck {
			return mms.ObjectAccessUnsupported
		}
		position := scl.Open
		if command.Value {
			position = scl.Closed
		}
		value, e := position.MMSValue()
		if e != nil {
			return e
		}
		q, _ := ied.Quality(0).MMSValue()
		return bay.Update(ctx, status.Sample(value, q, data.UTC(time.Now()))...)
	}); err != nil {
		return err
	}

	first, err := system.SVInput("BayIED", "S1", "I1")
	if err != nil {
		return err
	}
	source := simulator.SampledValues{Profile: first.Profile}
	var currentFields, voltageFields []sv.Field
	for phase := 1; phase <= 3; phase++ {
		current, e := system.SVInput("BayIED", "S1", fmt.Sprintf("I%d", phase))
		if e != nil {
			return e
		}
		voltage, e := system.SVInput("BayIED", "S1", fmt.Sprintf("U%d", phase))
		if e != nil {
			return e
		}
		currentFields = append(currentFields, current.Field)
		voltageFields = append(voltageFields, voltage.Field)
	}
	// The fixture uses 1 mA / 10 mV sample units. Scaling is application data,
	// not something that can be guessed from a raw INT32 SV sample.
	source.Sample = func(ctx context.Context, counter uint16, raw []byte) error {
		for phase := range currentFields {
			angle := 2 * math.Pi * (float64(counter%80)/80 - float64(phase)/3)
			if e := currentFields[phase].Encode(raw, data.Integer(int64(10000*math.Sqrt2*math.Sin(angle)))); e != nil {
				return e
			}
			if e := voltageFields[phase].Encode(raw, data.Integer(int64(23000*math.Sqrt2*math.Sin(angle)))); e != nil {
				return e
			}
		}
		return ctx.Err()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready, closed := make(chan struct{}, 1), make(chan struct{}, 1)
	var total float64
	count := 0
	station, err := simulator.StartStation(ctx, system, simulator.StationOptions{
		Models:       map[string]*scl.IED{"BayIED/S1": bay},
		Sources:      map[string]simulator.SampledValues{first.Profile.Reference: source},
		StrictInputs: true,
		OnSample: func(ctx context.Context, group simulator.SampleEvent) error {
			if group.Receiver != "BayIED" {
				return nil
			}
			var currents, voltages [3]float64
			for _, input := range group.Inputs {
				if !input.Valid {
					total, count = 0, 0
					q, _ := ied.Quality(ied.OldData).MMSValue()
					return bay.Update(ctx, mms.Assignment{Name: power.Quality, Value: q})
				}
				if len(input.Address) != 2 {
					continue
				}
				phase, e := strconv.Atoi(input.Address[1:])
				if e != nil || phase < 1 || phase > 3 {
					return fmt.Errorf("unknown phase input %s", input.Address)
				}
				number, e := input.Value.Int()
				if e != nil {
					return e
				}
				if input.Address[0] == 'I' {
					currents[phase-1] = float64(number) * 0.001
				}
				if input.Address[0] == 'U' {
					voltages[phase-1] = float64(number) * 0.01
				}
			}
			for phase := range currents {
				total += currents[phase] * voltages[phase]
			}
			count++
			if count == 80 {
				q, _ := ied.Quality(0).MMSValue()
				if e := bay.Update(ctx, power.Sample(data.Float32(float32(total/80)), q, data.UTC(time.Now()))...); e != nil {
					return e
				}
				total, count = 0, 0
				select {
				case ready <- struct{}{}:
				default:
				}
			}
			return nil
		},
		OnGOOSE: func(_ context.Context, event simulator.GOOSEEvent) error {
			for _, input := range event.Inputs {
				if input.Address != "breakerPosition" || !input.Valid {
					continue
				}
				position, e := scl.ReadPosition(input.Value)
				if e != nil {
					return e
				}
				if position == scl.Closed {
					select {
					case closed <- struct{}{}:
					default:
					}
				}
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	defer station.Close()
	address := ""
	for _, device := range station.Devices() {
		if device.IED == "BayIED" {
			address = device.Runtime.Address()
		}
	}
	client, err := ied.Dial(ctx, address, ied.Config{})
	if err != nil {
		return err
	}
	defer client.Close()
	select {
	case <-ready:
	case <-station.Done():
		return station.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
	value, err := client.Read(ctx, power.Value)
	if err != nil {
		return err
	}
	fmt.Println("MU -> SV -> bay calculation -> MMS total power:", value)
	report, err := client.SubscribeURCB(ctx, mms.DomainName("BayIEDCTRL", "LLN0$RP$events01"), ied.ReportOptions{})
	if err != nil {
		return err
	}
	defer report.Close()
	if _, err = client.Operate(ctx, "BayIEDCTRL/XCBR1.Pos", data.Boolean(true), ied.ControlOptions{}); err != nil {
		return err
	}
	select {
	case event := <-report.Events():
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
	case <-station.Done():
		return station.Err()
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
