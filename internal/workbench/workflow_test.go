// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"context"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
)

// T-WB-01 (backend half): connect → bind → read → create dataset → rebind an
// RCB → verify a selection → disconnect, over the same HTTP surface the
// browser uses.
func TestAcquisitionWorkflow(t *testing.T) {
	m, id := testDevice(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	sim, err := m.Start(id, SimulationOptions{Listen: "127.0.0.1:0", Controls: true})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := m.Connect(ctx, sim.Address, id)
	if err != nil {
		t.Fatal(err)
	}
	// Bind the engineered position dataset and read through the generation.
	info, err := m.BindRemote(ctx, conn.ID, "BayIEDCTRL", "LLN0$dsPos", true)
	if err != nil {
		t.Fatal(err)
	}
	if info.DataSet != "BayIEDCTRL/LLN0$dsPos" || info.Members == 0 || info.GenerationID != 0 {
		t.Fatalf("unexpected binding info: %+v", info)
	}
	snapshot, err := m.ReadRemoteBinding(ctx, conn.ID, "BayIEDCTRL", "LLN0$dsPos")
	if err != nil || len(snapshot.Entries) == 0 {
		t.Fatal(snapshot, err)
	}
	if list, err := m.RemoteBindings(conn.ID); err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	// Duplicate binds are refused instead of replacing silently.
	if _, err = m.BindRemote(ctx, conn.ID, "BayIEDCTRL", "LLN0$dsPos", true); err == nil {
		t.Fatal("duplicate binding must conflict")
	}
	// Create a dynamic dataset and rebind the events report block to it.
	if err = m.CreateRemoteDataSet(ctx, conn.ID, "BayIEDCTRL", "LLN0$dsDyn", []MemberAccess{{Domain: "BayIEDCTRL", Item: "XCBR1$ST$Pos$stVal"}}); err != nil {
		t.Fatal(err)
	}
	configured, err := m.ConfigureRemoteRCB(ctx, conn.ID, "BayIEDCTRL", "LLN0$RP$events01", "BayIEDCTRL", "LLN0$dsDyn", "")
	if err != nil {
		t.Fatal(err)
	}
	if configured.Declaration.DataSet != "BayIEDCTRL/LLN0$dsDyn" || configured.Declaration.ConfRev < 1 {
		t.Fatalf("unexpected reconfiguration: %+v", configured.Declaration)
	}
	// Revalidate a saved selection: one live point and one that never existed.
	doc := ied.NewSelectionDocument([]ied.SelectedPoint{
		{Domain: "BayIEDCTRL", Item: "XCBR1$ST$Pos$stVal", FC: "ST", Type: configured.Types[0], Origin: "online"},
		{Domain: "BayIEDCTRL", Item: "XCBR1$ST$Ghost$stVal", FC: "ST", Type: configured.Types[0], Origin: "online"},
	})
	report, err := m.VerifyRemoteSelection(ctx, conn.ID, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 2 || report.Entries[0].Status != ied.SelectionOK || report.Entries[1].Status != ied.SelectionMissing {
		t.Fatalf("unexpected verification: %+v", report.Entries)
	}
	// Unbind and disconnect release everything without remote side effects.
	if err = m.UnbindRemote(conn.ID, "BayIEDCTRL", "LLN0$dsPos"); err != nil {
		t.Fatal(err)
	}
	if err = m.UnbindRemote(conn.ID, "BayIEDCTRL", "LLN0$dsPos"); err == nil {
		t.Fatal("unbinding twice must not succeed")
	}
	if _, err = m.ReadRemoteBinding(ctx, conn.ID, "BayIEDCTRL", "LLN0$dsPos"); err == nil {
		t.Fatal("reading through a closed binding must fail")
	}
	if err = m.Disconnect(conn.ID); err != nil {
		t.Fatal(err)
	}
}
