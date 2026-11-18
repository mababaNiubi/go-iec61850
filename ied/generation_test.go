// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
)

// configReport builds an RPT information report with a configurable ConfRev,
// following the same layout rawReport uses in report_test.go.
func configReport(sequence uint8, confRev uint64) mms.InformationReport {
	list := mms.VMDName("RPT")
	options, _ := mms.BitString([]byte{0x48, 0x80}, 10)
	inclusion, _ := mms.BitString([]byte{0x80}, 1)
	values := []mms.Value{mms.VisibleString("Events"), options, mms.Unsigned(uint64(sequence)), mms.VisibleString("LD/LLN0$Events"), mms.Unsigned(confRev), inclusion, mms.Boolean(true)}
	report := mms.InformationReport{List: &list}
	for _, value := range values {
		report.Results = append(report.Results, mms.ReadResult{Value: value})
	}
	return report
}

func TestDecoderReportsConfigurationChange(t *testing.T) {
	name := mms.DomainName("LD", "GGIO1$ST$Ind1$stVal")
	decoder, err := NewReportDecoder(ReportConfig{ID: "Events", DataSet: "LD/LLN0$Events", ConfRev: 1, Members: []mms.VariableAccess{mms.Access(name)}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = decoder.Decode(configReport(1, 2))
	if !errors.Is(err, ErrConfigurationChanged) {
		t.Fatalf("expected a typed configuration change, got %v", err)
	}
	var change *GenerationChangeError
	if !errors.As(err, &change) || change.What != "report ConfRev" {
		t.Fatalf("expected revision details, got %+v", change)
	}
	// A changed dataset name is reported the same way.
	decoder, err = NewReportDecoder(ReportConfig{ID: "Events", DataSet: "LD/LLN0$Events", ConfRev: 1, Members: []mms.VariableAccess{mms.Access(name)}})
	if err != nil {
		t.Fatal(err)
	}
	bad := configReport(1, 1)
	bad.Results[3].Value = mms.VisibleString("LD/LLN0$Other")
	if _, err = decoder.Decode(bad); !errors.Is(err, ErrConfigurationChanged) {
		t.Fatalf("expected a typed dataset change, got %v", err)
	}
}

// A subscription terminates after delivering the change evidence, so a late
// report can never update the previous generation's consumers.
func TestSubscriptionTerminatesOnConfigurationChange(t *testing.T) {
	name := mms.DomainName("LD", "GGIO1$ST$Ind1$stVal")
	decoder, err := NewReportDecoder(ReportConfig{ID: "Events", DataSet: "LD/LLN0$Events", ConfRev: 1, Members: []mms.VariableAccess{mms.Access(name)}})
	if err != nil {
		t.Fatal(err)
	}
	sub := &ReportSubscription{decoder: decoder, events: make(chan ReportEvent, 1)}
	sub.accept(configReport(1, 2))
	event, open := <-sub.events
	if !open || !errors.Is(event.Err, ErrConfigurationChanged) {
		t.Fatalf("expected the change evidence, got %+v open=%t", event, open)
	}
	if _, open = <-sub.events; open {
		t.Fatal("stream must terminate after a configuration change")
	}
	if !errors.Is(sub.Err(), ErrConfigurationChanged) {
		t.Fatalf("terminal error is %v", sub.Err())
	}
}

// generationPeer starts one server with two clients sharing a gated store, so
// the second client can reconfigure declarations while the first is between
// its generation and verification reads.
func generationPeer(t *testing.T) (*Client, *Client, *peerStore, chan struct{}) {
	t.Helper()
	model := mms.NewModel()
	gate := make(chan struct{})
	started := make(chan struct{})
	store := &peerStore{Model: model, readGate: gate, readStarted: started}
	add := func(item string, value mms.Value, spec mms.TypeSpec, writable bool) {
		t.Helper()
		if err := model.Add(mms.Variable{Name: mms.DomainName("LD", item), Value: value, Type: spec, Writable: writable}); err != nil {
			t.Fatal(err)
		}
	}
	add("GGIO1$ST$SPCSO1$stVal", mms.Boolean(false), mms.TypeSpec{Kind: mms.KindBoolean}, false)
	add("GGIO1$ST$Ind1$stVal", mms.Boolean(true), mms.TypeSpec{Kind: mms.KindBoolean}, false)
	add("LLN0$RP$Events01$RptEna", mms.Boolean(false), mms.TypeSpec{Kind: mms.KindBoolean}, true)
	add("LLN0$RP$Events01$Resv", mms.Boolean(false), mms.TypeSpec{Kind: mms.KindBoolean}, true)
	add("LLN0$RP$Events01$RptID", mms.VisibleString("Events"), mms.TypeSpec{Kind: mms.KindVisibleString, Length: -64}, false)
	add("LLN0$RP$Events01$DatSet", mms.VisibleString("LD/LLN0$Dyn"), mms.TypeSpec{Kind: mms.KindVisibleString, Length: -64}, false)
	add("LLN0$RP$Events01$ConfRev", mms.Unsigned(1), mms.TypeSpec{Kind: mms.KindUnsigned, Width: 32}, true)
	server, err := mms.NewServer(store, mms.Config{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Close()
		<-done
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	dial := func() *Client {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		client, err := Dial(ctx, listener.Addr().String(), Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { client.Close() })
		return client
	}
	return dial(), dial(), store, gate
}

func TestVerifiedSnapshotDetectsMemberChange(t *testing.T) {
	reader, writer, store, gate := generationPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list := mms.DomainName("LD", "LLN0$Dyn")
	if err := writer.raw.DefineNamedList(ctx, list, []mms.VariableAccess{mms.Access(mms.DomainName("LD", "GGIO1$ST$SPCSO1$stVal"))}); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := reader.ReadDataSetVerified(ctx, list, GenerationPolicy{})
		result <- err
	}()
	// The value read blocks on the gate; replace the dataset underneath it.
	select {
	case <-store.readStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("value read never started")
	}
	if _, err := writer.raw.DeleteNamedList(ctx, list); err != nil {
		t.Fatal(err)
	}
	if err := writer.raw.DefineNamedList(ctx, list, []mms.VariableAccess{mms.Access(mms.DomainName("LD", "GGIO1$ST$Ind1$stVal"))}); err != nil {
		t.Fatal(err)
	}
	close(gate)
	err := <-result
	if !errors.Is(err, ErrConfigurationChanged) {
		t.Fatalf("expected a typed member change, got %v", err)
	}
	var change *GenerationChangeError
	if !errors.As(err, &change) || change.What == "" {
		t.Fatalf("expected member change details, got %+v", change)
	}
}

func TestVerifiedSnapshotDetectsConfRevChange(t *testing.T) {
	reader, writer, store, gate := generationPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list := mms.DomainName("LD", "LLN0$Dyn")
	if err := writer.raw.DefineNamedList(ctx, list, []mms.VariableAccess{mms.Access(mms.DomainName("LD", "GGIO1$ST$SPCSO1$stVal"))}); err != nil {
		t.Fatal(err)
	}
	block := mms.DomainName("LD", "LLN0$RP$Events01")
	result := make(chan error, 1)
	go func() {
		_, err := reader.ReadDataSetVerified(ctx, list, GenerationPolicy{VerifyRCB: block})
		result <- err
	}()
	select {
	case <-store.readStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("value read never started")
	}
	if err := writer.Write(ctx, mms.DomainName("LD", "LLN0$RP$Events01$ConfRev"), mms.Unsigned(2)); err != nil {
		t.Fatal(err)
	}
	close(gate)
	err := <-result
	var change *GenerationChangeError
	if !errors.As(err, &change) || change.What != "RCB ConfRev" {
		t.Fatalf("expected an RCB ConfRev change, got %v", err)
	}
}

func TestVerifiedSnapshotStablePeer(t *testing.T) {
	reader, writer, _, gate := generationPeer(t)
	close(gate) // no mutation: let the value read pass immediately
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list := mms.DomainName("LD", "LLN0$Dyn")
	if err := writer.raw.DefineNamedList(ctx, list, []mms.VariableAccess{mms.Access(mms.DomainName("LD", "GGIO1$ST$SPCSO1$stVal"))}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := reader.ReadDataSetVerified(ctx, list, GenerationPolicy{VerifyTypes: true, VerifyRCB: mms.DomainName("LD", "LLN0$RP$Events01")})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Verified || snapshot.ConfRev != 1 || len(snapshot.Entries) != 1 {
		t.Fatalf("unexpected snapshot: verified=%t rev=%d entries=%d", snapshot.Verified, snapshot.ConfRev, len(snapshot.Entries))
	}
	// An unverified one-shot read keeps its released contract: revision stays
	// unknown rather than borrowing the RCB's value.
	plain, err := reader.ReadDataSet(ctx, list)
	if err != nil || plain.Verified || plain.ConfRev != 0 {
		t.Fatalf("plain read contract changed: %+v err=%v", plain, err)
	}
}
