// SPDX-License-Identifier: GPL-3.0-or-later
// Separate ordinary SP configuration from transactional SG/SE editing.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
	"github.com/mababaNiubi/go-iec61850/simulator"
)

func run() error {
	system, err := scl.ParseFile("examples/ied/settings/device.scd")
	if err != nil {
		return err
	}
	model, err := system.Device("BayIED", "S1")
	if err != nil {
		return err
	}
	delay, err := model.Lookup("CTRL", "PTOC1", "SP", "OpDlTmms.setVal")
	if err != nil {
		return err
	}
	// Validate the process range before committing the remotely written SP.
	// Replace the callback's successful path with the equipment configuration
	// driver. Returning an error leaves the model at the previous setting.
	if err = model.OnSetpoint(delay, func(ctx context.Context, value data.Value) error {
		ms, err := value.Int()
		if err != nil || ms < 0 || ms > 5000 {
			return mms.ObjectValueInvalid
		}
		return ctx.Err()
	}); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runtime, err := simulator.Start(ctx, model, simulator.Options{Listen: "127.0.0.1:0", GOOSE: simulator.GOOSEOff})
	if err != nil {
		return err
	}
	defer runtime.Close()
	client, err := ied.Dial(ctx, runtime.Address(), ied.Config{})
	if err != nil {
		return err
	}
	defer client.Close()
	if err = client.Write(ctx, delay, data.Integer(250)); err != nil {
		return err
	}
	active, err := model.Lookup("CTRL", "PTOC1", "SG", "StrVal.setMag.f")
	if err != nil {
		return err
	}
	edit, err := model.Lookup("CTRL", "PTOC1", "SE", "StrVal.setMag.f")
	if err != nil {
		return err
	}
	result, err := client.EditSettings(ctx, active.Domain, 2, map[mms.ObjectName]data.Value{edit: data.Float32(25)}, false)
	if err != nil {
		return err
	}
	value, err := client.Read(ctx, active)
	if err != nil {
		return err
	}
	fmt.Printf("Group 2 committed=%t; active group 1 threshold=%s\n", result.Committed, value)
	// Activation is explicit. This changes the modeled active SG view only
	// after the edit copy has been validated and confirmed.
	activate := mms.DomainName(active.Domain, "LLN0$SP$SGCB$ActSG")
	if err = client.Write(ctx, activate, data.Unsigned(2)); err != nil {
		return err
	}
	value, err = client.Read(ctx, active)
	if err != nil {
		return err
	}
	fmt.Println("Active group 2 threshold:", value)
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
