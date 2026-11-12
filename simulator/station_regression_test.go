// SPDX-License-Identifier: GPL-3.0-or-later
package simulator_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/internal/sample"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
	"github.com/mababaNiubi/go-iec61850/simulator"
)

func waitInput(t *testing.T, ctx context.Context, station *simulator.Station, predicate func(simulator.InputReading) bool) simulator.InputReading {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, input := range station.Inputs() {
			if predicate(input) {
				return input
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("input not observed", ctx.Err(), station.Inputs())
			return simulator.InputReading{}
		case <-ticker.C:
		}
	}
}

func TestStationRespectsNetworkIsolationAndOptionalIntAddr(t *testing.T) {
	raw := sample.StationSCL()
	system, err := scl.Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := system.ServerAccessPoint("BayIED", "P1")
	if err != nil || owner != "S1" {
		t.Fatal(owner, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	broken := bytes.Replace(raw, []byte(`<ConnectedAP iedName="BayIED" apName="P1"/>`), nil, 1)
	unwired, err := scl.Parse(bytes.NewReader(broken))
	if err != nil {
		t.Fatal(err)
	}
	station, err := simulator.StartStation(ctx, unwired, simulator.StationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer station.Close()
	input := waitInput(t, ctx, station, func(v simulator.InputReading) bool { return v.Address == "I1" })
	if input.Valid || input.State != "unbound" || !strings.Contains(input.Error, "SubNetwork") {
		t.Fatal(input)
	}
	if strict, e := simulator.StartStation(ctx, unwired, simulator.StationOptions{StrictInputs: true}); e == nil {
		strict.Close()
		t.Fatal("strict startup accepted disconnected inputs")
	}
	noAddr := bytes.Replace(raw, []byte(` intAddr="breakerPosition"`), nil, 1)
	system, err = scl.Parse(bytes.NewReader(noAddr))
	if err != nil {
		t.Fatal(err)
	}
	bound, err := simulator.StartStation(ctx, system, simulator.StationOptions{StrictInputs: true})
	if err != nil {
		t.Fatal(err)
	}
	defer bound.Close()
	input = waitInput(t, ctx, bound, func(v simulator.InputReading) bool { return v.Service == "GOOSE" && v.Valid })
	if input.Address == "" || input.Reference == "" || input.Network != "StationBus" {
		t.Fatal(input)
	}
}

func TestStationPublishesQualityExpiryAndCoherentSampleGroups(t *testing.T) {
	system, err := scl.Parse(bytes.NewReader(sample.StationSCL()))
	if err != nil {
		t.Fatal(err)
	}
	mu, err := system.Device("MUA", "AP1")
	if err != nil {
		t.Fatal(err)
	}
	value, err := mu.Lookup("LD0", "TCTR1", "MX", "Amp.instMag.i")
	if err != nil {
		t.Fatal(err)
	}
	quality, err := mu.Lookup("LD0", "TCTR1", "MX", "Amp.q")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q, _ := ied.Quality(ied.OldData).MMSValue()
	if err = mu.Update(ctx, mms.Assignment{Name: value, Value: mms.Integer(1234)}, mms.Assignment{Name: quality, Value: q}); err != nil {
		t.Fatal(err)
	}
	groups := make(chan simulator.SampleEvent, 4)
	station, err := simulator.StartStation(ctx, system, simulator.StationOptions{
		Models: map[string]*scl.IED{"MUA/AP1": mu}, InputTimeout: 100 * time.Millisecond, NominalFrequency: 60,
		OnSample: func(_ context.Context, event simulator.SampleEvent) error {
			select {
			case groups <- event:
			default:
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer station.Close()
	bad := waitInput(t, ctx, station, func(v simulator.InputReading) bool { return v.Address == "I1" && v.State == "quality" })
	if bad.Valid || !bad.HasQuality || bad.Quality != ied.OldData || bad.Value.String() != "1234" {
		t.Fatal(bad)
	}
	select {
	case group := <-groups:
		if len(group.Inputs) != 6 {
			t.Fatal("sample callback split one ASDU", group)
		}
		for _, v := range group.Inputs {
			if v.Counter != group.Event.ASDU.SampleCount || v.ReceivedAt != group.Event.ReceivedAt {
				t.Fatal("mixed ASDU group", group)
			}
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	q, _ = ied.Quality(0).MMSValue()
	if err = mu.Update(ctx, mms.Assignment{Name: quality, Value: q}); err != nil {
		t.Fatal(err)
	}
	good := waitInput(t, ctx, station, func(v simulator.InputReading) bool { return v.Address == "I1" && v.Valid })
	if good.LastGoodAt.IsZero() {
		t.Fatal(good)
	}
	for _, d := range station.Devices() {
		if d.IED == "MUA" {
			if e := d.Runtime.Close(); e != nil {
				t.Fatal(e)
			}
		}
	}
	expired := waitInput(t, ctx, station, func(v simulator.InputReading) bool { return v.Address == "I1" && v.State == "expired" })
	if expired.Valid || expired.Value.String() != "1234" || expired.LastGoodAt.IsZero() || expired.Error == "" {
		t.Fatal(expired)
	}
	if !expired.ValidUntil.Before(time.Now()) {
		t.Fatal("expiry changed its original deadline", expired)
	}
}
