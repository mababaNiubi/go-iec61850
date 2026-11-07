// SPDX-License-Identifier: GPL-3.0-or-later
// Run from the repository root. With no flags this creates a virtual breaker
// and controls it over actual MMS/TCP. External IEDs use the same public API.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
	"github.com/mababaNiubi/go-iec61850/simulator"
)

func run() error {
	address := flag.String("address", "", "external IED host:port; empty starts the virtual sample")
	file := flag.String("file", "internal/sample/bay.scd", "SCL for the local BayIED sample")
	reference := flag.String("object", "BayIEDCTRL/XCBR1.Pos", "domain/LN.controlObject")
	block := flag.String("urcb", "", "optional engineered URCB, e.g. LLN0.Events01 in the control's domain")
	on := flag.Bool("on", true, "binary ctlVal: true closes the sample breaker")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := ied.Config{Origin: ied.Origin{Category: 2, ID: []byte("example-station")}}
	if *address == "" {
		if *block == "" {
			*block = "LLN0.events01"
		}
		system, err := scl.ParseFile(*file)
		if err != nil {
			return err
		}
		model, err := system.Device("BayIED", "S1")
		if err != nil {
			return err
		}
		oper, err := model.Lookup("CTRL", "XCBR1", "CO", "Pos.Oper")
		if err != nil {
			return err
		}
		feedback, err := model.Signal("CTRL", "XCBR1", "ST", "Pos", "stVal")
		if err != nil {
			return err
		}
		// Replace this virtual actuator with your hardware driver. An accepted
		// command and an independently acquired position are different evidence.
		err = model.OnOperate(oper, func(ctx context.Context, command scl.Command) error {
			if command.Test || command.SynchroCheck {
				return mms.ObjectAccessUnsupported
			}
			position := scl.Open
			if command.Value {
				position = scl.Closed
			}
			value, err := position.MMSValue()
			if err != nil {
				return err
			}
			quality, err := ied.Quality(0).MMSValue()
			if err != nil {
				return err
			}
			return model.Update(ctx, feedback.Sample(value, quality, mms.UTC(time.Now()))...)
		})
		if err != nil {
			return err
		}
		runtime, err := simulator.Start(ctx, model, simulator.Options{Listen: "127.0.0.1:0", GOOSE: simulator.GOOSEMemory})
		if err != nil {
			return err
		}
		defer runtime.Close()
		*address = runtime.Address()
		cfg.MMS = model.ClientConfig()
		fmt.Println("Virtual IED:", *address)
	}
	client, err := ied.Dial(ctx, *address, cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	domain, object, ok := strings.Cut(*reference, "/")
	if !ok {
		return fmt.Errorf("expected domain/LN.object")
	}
	var subscription *ied.ReportSubscription
	if *block != "" {
		name, err := ied.ObjectName(domain, "RP", *block)
		if err != nil {
			return err
		}
		subscription, err = client.SubscribeURCB(ctx, name, ied.ReportOptions{})
		if err != nil {
			return err
		}
		defer subscription.Close()
	}
	// Control reads ctlModel and the online Oper declaration. Modes 1..4 use
	// their own select/operate/termination flow, without application locking.
	result, err := client.Operate(ctx, *reference, data.Boolean(*on), ied.ControlOptions{InterlockCheck: true})
	if err != nil {
		return err
	}
	fmt.Printf("Control: selected=%t acknowledged=%t terminated=%t\n", result.Selected, result.Acknowledged, result.Terminated)
	status, err := ied.ObjectName(domain, "ST", object+".stVal")
	if err != nil {
		return err
	}
	value, err := client.Read(ctx, status)
	if err != nil {
		return err
	}
	fmt.Println("Independent status:", value)
	if subscription != nil {
		select {
		case event, ok := <-subscription.Events():
			if !ok {
				return subscription.Err()
			}
			if event.Err != nil {
				return event.Err
			}
			fmt.Printf("URCB %s: %d included members\n", event.Report.ID, len(event.Report.Entries))
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := subscription.Stop(ctx); err != nil {
			return err
		}
	}
	return client.Conclude(ctx)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
