// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
)

func TestWholeStationUsesSharedGOOSEAndModelSampledValues(t *testing.T) {
	m := New()
	defer m.Close()
	raw, err := os.ReadFile("../../examples/scd/station/station.scd")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := m.Import("station.scd", raw)
	if err != nil {
		t.Fatal(err)
	}
	station, err := m.StartStation(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(station.Devices) != 3 {
		t.Fatal(station)
	}
	var bay, mu DeviceSummary
	for _, d := range station.Devices {
		switch d.IED {
		case "BayIED":
			bay = d
		case "MUA":
			mu = d
		}
	}
	if err = m.Stop(bay.ID); err == nil {
		t.Fatal("individual stop bypassed station ownership")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	muDevice, err := m.get(mu.ID)
	if err != nil {
		t.Fatal(err)
	}
	name, err := muDevice.ied.Lookup("MU", "TCTR1", "MX", "Amp.instMag.i")
	if err != nil {
		// The selected SCL is the authority for the MU's logical-device name.
		for _, p := range muDevice.points {
			if p.FC == "MX" && p.Name.Item == "TCTR1$MX$Amp$instMag$i" {
				name = p.Name
				err = nil
				break
			}
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = muDevice.ied.Update(ctx, mms.Assignment{Name: name, Value: mms.Integer(1234)}); err != nil {
		t.Fatal(err)
	}
	peer, err := ied.Dial(ctx, bay.Address, ied.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if _, err = peer.Operate(ctx, "BayIEDCTRL/XCBR1.Pos", mms.Boolean(true), ied.ControlOptions{}); err != nil {
		t.Fatal(err)
	}
	for {
		states := m.Stations()
		if len(states) != 1 {
			t.Fatal(states)
		}
		gotSV, gotGOOSE := false, false
		for _, input := range states[0].Inputs {
			if input.Service == "SV" && input.Address == "I1" && input.Value == "1234" && input.Error == "" {
				gotSV = true
			}
			if input.Service == "GOOSE" && input.Address == "breakerPosition" && input.Value == "bit-string(80)" && input.Error == "" {
				gotGOOSE = true
			}
		}
		if gotSV && gotGOOSE {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("missing inputs: %+v", states[0])
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err = m.StopStation(doc.ID); err != nil {
		t.Fatal(err)
	}
	if len(m.Stations()) != 0 {
		t.Fatal("stopped station retained")
	}
	// A clean stop relinquishes model/transport ownership for another run.
	if _, err = m.StartStation(doc.ID); err != nil {
		t.Fatal(err)
	}
}
