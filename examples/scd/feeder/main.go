// SPDX-License-Identifier: GPL-3.0-or-later
// EX-01: a complete feeder acquisition workflow over public packages only.
//
// The modeled bay provides XCBR position/control, MMXU measurements and one
// BRCB. The client crosses real loopback TCP. The flow demonstrates, in
// order: verified dataset binding, buffered reporting with a caller-owned
// cursor, control with modeled feedback, deterministic failure paths
// (invalid target, stale binding after reconfiguration) and selection
// revalidation after reconnect. Storage is a caller-owned slice: a real
// application substitutes its own database. Nothing here imports the
// workbench.
//
// Run: go run ./examples/scd/feeder
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
	samplemodel "github.com/mababaNiubi/go-iec61850/internal/sample"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
	"github.com/mababaNiubi/go-iec61850/simulator"
)

// sample is one durably handled report: the caller stores it, then the
// cursor may advance. EntryID moves only after the append succeeds.
type sample struct {
	sequence uint16
	values   []string
}

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// --- modeled feeder over loopback TCP ---------------------------------
	system, err := scl.Parse(bytes.NewReader(samplemodel.SCL()))
	if err != nil {
		return err
	}
	model, err := system.Device("BayIED", "S1")
	if err != nil {
		return err
	}
	// The application owns the actuator: bind the equipment behavior to the
	// modeled control object before serving. Feedback is independent, local
	// acquisition through Update, so q/t stay coherent with the new status.
	if err = bindActuator(model); err != nil {
		return err
	}
	runtime, err := simulator.Start(ctx, model, simulator.Options{Listen: "127.0.0.1:0", GOOSE: simulator.GOOSEOff})
	if err != nil {
		return err
	}
	defer runtime.Close()

	client, err := ied.Dial(ctx, runtime.Address(), ied.Config{Origin: ied.Origin{Category: 2, ID: []byte("feeder-example")}})
	if err != nil {
		return err
	}
	step("connected to %s", runtime.Address())

	// --- verified acquisition binding -------------------------------------
	// dsPos carries XCBR position with q/t in one FCD. Each-read verification
	// rechecks the declaration after the values, so a reconfigured peer is
	// rejected instead of decoded under a stale generation.
	binding, err := client.BindDataSet(ctx, mms.DomainName("BayIEDCTRL", "LLN0$dsPos"), ied.BindOptions{VerifyEachRead: true})
	if err != nil {
		return err
	}
	defer binding.Close()
	snapshot, err := binding.Read(ctx)
	if err != nil {
		return err
	}
	step("bound dsPos generation %d, verified=%t, %d members", binding.Info().GenerationID, snapshot.Verified, len(snapshot.Entries))

	// --- buffered reporting with a caller-owned cursor --------------------
	store := []sample{}
	var cursor []byte
	sub, err := client.SubscribeBRCB(ctx, mms.DomainName("BayIEDCTRL", "LLN0$BR$history01"), ied.ReportOptions{GI: true})
	if err != nil {
		return err
	}
	// Drain one GI report; a real application runs this loop until shutdown.
	select {
	case event := <-sub.Events():
		if event.Err != nil {
			return event.Err
		}
		entry := sample{sequence: deref(event.Report.SequenceNumber)}
		for _, leaf := range event.Report.Entries {
			entry.values = append(entry.values, leaf.Variable.Name.Item+"="+leaf.Value.String())
		}
		store = append(store, entry)
		cursor = event.Report.EntryID // save only after durable handling
		step("stored report seq=%d EntryID=%x (%d members)", entry.sequence, cursor, len(entry.values))
	case <-time.After(5 * time.Second):
		return errors.New("no general-interrogation report")
	}

	// --- control with modeled feedback ------------------------------------
	result, err := client.Operate(ctx, "BayIEDCTRL/XCBR1.Pos", mms.Boolean(true), ied.ControlOptions{InterlockCheck: true})
	if err != nil {
		// A refused or uncertain operation is surfaced, never retried
		// automatically: duplicating an equipment command after an uncertain
		// outcome is an application decision.
		step("operate refused: %v", err)
	} else {
		step("operate acknowledged=%t terminated=%t", result.Acknowledged, result.Terminated)
	}
	// A refused path surfaces the peer error unchanged; nothing retries an
	// equipment operation automatically.
	if _, err = client.Operate(ctx, "BayIEDCTRL/MMXU1.Nothing", mms.Boolean(true), ied.ControlOptions{}); err != nil {
		step("invalid control target refused: %v", err)
	}

	// --- dynamic dataset, reconfiguration and stale bindings --------------
	if err = client.CreateDataSet(ctx, mms.DomainName("BayIEDCTRL", "LLN0$dsDemo"), []mms.VariableAccess{
		mms.Access(mms.DomainName("BayIEDCTRL", "XCBR1$ST$Pos$stVal")),
	}); err != nil {
		return err
	}
	demo, err := client.BindDataSet(ctx, mms.DomainName("BayIEDCTRL", "LLN0$dsDemo"), ied.BindOptions{VerifyEachRead: true})
	if err != nil {
		return err
	}
	if _, err = demo.Read(ctx); err != nil {
		return err
	}
	step("bound dynamic dsDemo")
	// Reconfigure the dataset underneath the binding.
	if _, err = client.DeleteDataSet(ctx, mms.DomainName("BayIEDCTRL", "LLN0$dsDemo")); err != nil {
		return err
	}
	if err = client.CreateDataSet(ctx, mms.DomainName("BayIEDCTRL", "LLN0$dsDemo"), []mms.VariableAccess{
		mms.Access(mms.DomainName("BayIEDCTRL", "MMXU1$MX$TotW$mag$f")),
	}); err != nil {
		return err
	}
	if _, err = demo.Read(ctx); !errors.Is(err, ied.ErrConfigurationChanged) {
		return fmt.Errorf("expected a configuration change, got %v", err)
	}
	step("stale binding refused: %v", err)
	if _, err = demo.Refresh(ctx); err != nil {
		return err
	}
	snapshot, err = demo.Read(ctx)
	if err != nil {
		return err
	}
	step("refreshed to generation %d (%s)", demo.Info().GenerationID, snapshot.Entries[0].Variable.Name.Item)
	demo.Close()

	// --- reconnect: selection revalidation and cursor resume ---------------
	position := mms.DomainName("BayIEDCTRL", "XCBR1$ST$Pos$stVal")
	spec, err := client.GetVariableAccessAttributes(ctx, position)
	if err != nil {
		return err
	}
	document := ied.NewSelectionDocument([]ied.SelectedPoint{
		{Domain: position.Domain, Item: position.Item, FC: "ST", Type: spec, Origin: "online"},
	})
	if err = client.Close(); err != nil {
		return err
	}
	client, err = ied.Dial(ctx, runtime.Address(), ied.Config{Origin: ied.Origin{Category: 2, ID: []byte("feeder-example")}})
	if err != nil {
		return err
	}
	defer client.Close()
	step("reconnected")
	report, err := client.VerifySelection(ctx, document)
	if err != nil {
		return err
	}
	for _, entry := range report.Entries {
		step("selection %s: %s", entry.Reference, entry.Status)
	}
	if len(cursor) > 0 {
		if _, err = client.SubscribeBRCB(ctx, mms.DomainName("BayIEDCTRL", "LLN0$BR$history01"), ied.ReportOptions{ResumeEntryID: cursor}); err == nil {
			step("resumed from the acknowledged cursor")
		} else {
			// An unusable cursor is an explicit remote outcome; it never
			// purges retained history to recover silently.
			step("cursor resume refused: %v", err)
		}
	}
	step("done: %d stored samples, ownership returned, associations closed", len(store))
	return nil
}

// bindActuator connects the XCBR operate object to a simulated mechanism.
// Replace this with the real equipment driver; the protocol side is done.
func bindActuator(model *scl.IED) error {
	stVal := mms.DomainName("BayIEDCTRL", "XCBR1$ST$Pos$stVal")
	quality := mms.DomainName("BayIEDCTRL", "XCBR1$ST$Pos$q")
	stamp := mms.DomainName("BayIEDCTRL", "XCBR1$ST$Pos$t")
	return model.OnControl(mms.DomainName("BayIEDCTRL", "XCBR1$CO$Pos$Oper"), func(ctx context.Context, cmd scl.Command) error {
		if cmd.Test || cmd.SynchroCheck {
			return mms.ObjectAccessUnsupported
		}
		position := scl.Open
		if cmd.Value {
			position = scl.Closed
		}
		value, err := position.MMSValue()
		if err != nil {
			return err
		}
		q, err := model.Read(ctx, mms.Access(quality))
		if err != nil {
			return err
		}
		step("actuator: Pos ← %s (origin=%s ctlNum=%d)", position, cmd.OriginID, cmd.Number)
		return model.Update(ctx,
			mms.Assignment{Name: stVal, Value: value},
			mms.Assignment{Name: quality, Value: q},
			mms.Assignment{Name: stamp, Value: mms.UTC(time.Now())})
	})
}

func step(format string, args ...any) {
	log.Printf("feeder: "+format, args...)
}

func deref(v *uint16) uint16 {
	if v == nil {
		return 0
	}
	return *v
}
