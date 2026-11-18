// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/internal/sample"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

// sclPeer starts the modeled bay server so configuration workflows run
// against the same report engine applications deploy.
func sclPeer(t *testing.T) *Client {
	t.Helper()
	system, err := scl.Parse(bytes.NewReader(sample.SCL()))
	if err != nil {
		t.Fatal(err)
	}
	model, err := system.Device("BayIED", "S1")
	if err != nil {
		t.Fatal(err)
	}
	server, err := model.NewServer(mms.Config{})
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := Dial(ctx, listener.Addr().String(), Config{Origin: Origin{Category: 2, ID: []byte("configure-test")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

// T-DS-02/T-RCB-01: a dynamically created dataset binds an RCB; the peer
// bumps ConfRev itself, the re-read declaration reports it and a subsequent
// subscription delivers the bound members through a general interrogation.
func TestConfigureRCBBindsDynamicDataSet(t *testing.T) {
	client := sclPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dynamic := mms.DomainName("BayIEDCTRL", "LLN0$dsDynamic")
	position := mms.Access(mms.DomainName("BayIEDCTRL", "XCBR1$ST$Pos$stVal"))
	if err := client.CreateDataSet(ctx, dynamic, []mms.VariableAccess{position}); err != nil {
		t.Fatal(err)
	}
	block := mms.DomainName("BayIEDCTRL", "LLN0$RP$events01")
	before, err := client.ReadReportBlock(ctx, block)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := client.ConfigureRCB(ctx, block, RCBConfiguration{DataSet: dynamic})
	if err != nil {
		t.Fatal(err)
	}
	if configured.Declaration.DataSet != dynamic.String() {
		t.Fatalf("binding not accepted: %+v", configured.Declaration)
	}
	if configured.Declaration.ConfRev != before.ConfRev+1 {
		t.Fatalf("expected the peer to bump ConfRev: %d -> %d", before.ConfRev, configured.Declaration.ConfRev)
	}
	if len(configured.Members) != 1 || configured.Members[0].Name != position.Name {
		t.Fatalf("unexpected bound members: %+v", configured.Members)
	}
	// A bound generation decodes the live report without re-requesting layout.
	sub, err := client.SubscribeURCB(ctx, block, ReportOptions{GI: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		if err := sub.Stop(stopCtx); err != nil {
			t.Error(err)
		}
	}()
	select {
	case event := <-sub.Events():
		if event.Err != nil || len(event.Report.Entries) != 1 || event.Report.Entries[0].Variable.Name != position.Name {
			t.Fatalf("unexpected report: %+v err=%v", event.Report, event.Err)
		}
		if event.Report.ConfRev != configured.Declaration.ConfRev {
			t.Fatalf("report revision %d != configured %d", event.Report.ConfRev, configured.Declaration.ConfRev)
		}
	case <-ctx.Done():
		t.Fatal("no report after general interrogation")
	}
	// An enabled block refuses reconfiguration until its subscription stops.
	if _, err = client.ConfigureRCB(ctx, block, RCBConfiguration{DataSet: dynamic}); err == nil {
		t.Fatal("enabled report control block must refuse reconfiguration")
	}
}

func TestCreateDataSetValidatesMembers(t *testing.T) {
	client := sclPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dynamic := mms.DomainName("BayIEDCTRL", "LLN0$dsBroken")
	err := client.CreateDataSet(ctx, dynamic, []mms.VariableAccess{mms.Access(mms.DomainName("BayIEDCTRL", "XCBR1$ST$Missing$stVal"))})
	if err == nil {
		t.Fatal("invalid member must fail locally")
	}
	if _, e := client.GetNamedListAttributes(ctx, dynamic); e == nil {
		t.Fatal("nothing must be defined when validation fails")
	}
	// Association-private lists are not offered: they cannot back an RCB.
	if e := client.CreateDataSet(ctx, mms.AssociationName("dsAA"), []mms.VariableAccess{mms.Access(mms.DomainName("BayIEDCTRL", "XCBR1$ST$Pos$stVal"))}); e == nil {
		t.Fatal("association-scoped dataset names must be rejected")
	}
}

// Deleting a dynamic list removes the registry entry, and a later binding to
// it is refused by the peer instead of referencing freed members.
func TestDeleteDataSetRemovesBindingTarget(t *testing.T) {
	client := sclPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dynamic := mms.DomainName("BayIEDCTRL", "LLN0$dsTemp")
	position := mms.Access(mms.DomainName("BayIEDCTRL", "XCBR1$ST$Pos$stVal"))
	if err := client.CreateDataSet(ctx, dynamic, []mms.VariableAccess{position}); err != nil {
		t.Fatal(err)
	}
	result, err := client.DeleteDataSet(ctx, dynamic)
	if err != nil || result.Deleted != 1 {
		t.Fatal(result, err)
	}
	if _, err = client.GetNamedListAttributes(ctx, dynamic); err == nil {
		t.Fatal("deleted list must disappear")
	}
	block := mms.DomainName("BayIEDCTRL", "LLN0$RP$events01")
	if _, err = client.ConfigureRCB(ctx, block, RCBConfiguration{DataSet: dynamic}); err == nil {
		t.Fatal("binding to a deleted dataset must be refused by the peer")
	}
}

// Engineered static datasets are not remotely deletable; the result reports
// the match without deleting, mirroring the service semantics.
func TestStaticDataSetNotDeletable(t *testing.T) {
	client := sclPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	static := mms.DomainName("BayIEDCTRL", "LLN0$dsPos")
	result, err := client.DeleteDataSet(ctx, static)
	if err != nil || result.Matched != 1 || result.Deleted != 0 {
		t.Fatal(result, err)
	}
}
