// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/mms"
)

type peerStore struct {
	*mms.Model
	mu          sync.Mutex
	association *mms.Association
	mode        ControlModel
	sequence    uint8
	terminate   bool
	readGate    <-chan struct{}
	readStarted chan struct{}
	readOnce    sync.Once
}

func (s *peerStore) Read(ctx context.Context, access mms.VariableAccess) (mms.Value, error) {
	s.mu.Lock()
	gate, started := s.readGate, s.readStarted
	s.mu.Unlock()
	if access.Name.Item == "GGIO1$ST$SPCSO1$stVal" && gate != nil {
		s.readOnce.Do(func() { close(started) })
		select {
		case <-gate:
		case <-ctx.Done():
			return mms.Value{}, ctx.Err()
		}
	}
	return s.Model.Read(ctx, access)
}

func (s *peerStore) Write(ctx context.Context, access mms.VariableAccess, value mms.Value) error {
	if err := s.Model.Write(ctx, access, value); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if access.Name.Item == "LLN0$RP$Events01$RptEna" {
		on, _ := value.Bool()
		if on {
			report := rawReport(s.sequence)
			s.sequence++
			return s.association.SendReport(ctx, report)
		}
	}
	if access.Name.Item == "GGIO1$CO$SPCSO1$Oper" {
		// Send an unsolicited report before the confirmed Write response, then
		// a termination. The one dispatcher must deliver both independently.
		report := rawReport(s.sequence)
		s.sequence++
		if err := s.association.SendReport(ctx, report); err != nil {
			return err
		}
		if s.mode >= DirectEnhanced && s.terminate {
			return s.association.SendReport(ctx, mms.InformationReport{Variables: []mms.VariableAccess{access}, Results: []mms.ReadResult{{Value: value}}})
		}
	}
	return nil
}
func dialPeer(t *testing.T, mode ControlModel, terminate bool) (*Client, *peerStore) {
	t.Helper()
	model := mms.NewModel()
	store := &peerStore{Model: model, mode: mode, terminate: terminate}
	add := func(item string, value mms.Value, spec mms.TypeSpec, writable bool) {
		t.Helper()
		if err := model.Add(mms.Variable{Name: mms.DomainName("LD", item), Value: value, Type: spec, Writable: writable}); err != nil {
			t.Fatal(err)
		}
	}
	add("GGIO1$CF$SPCSO1$ctlModel", mms.Integer(int64(mode)), mms.TypeSpec{Kind: mms.KindInteger, Width: 8}, false)
	spec := controlType()
	command, err := BuildCommand(spec, data.Boolean(false), Origin{}, 0, ControlOptions{})
	if err != nil {
		t.Fatal(err)
	}
	add("GGIO1$CO$SPCSO1$Oper", command, spec, true)
	add("GGIO1$CO$SPCSO1$SBOw", command, spec, true)
	add("GGIO1$CO$SPCSO1$SBO", mms.VisibleString("LD/GGIO1$CO$SPCSO1"), mms.TypeSpec{Kind: mms.KindVisibleString, Length: -64}, false)
	add("GGIO1$ST$SPCSO1$stVal", mms.Boolean(false), mms.TypeSpec{Kind: mms.KindBoolean}, false)
	add("LLN0$RP$Events01$RptEna", mms.Boolean(false), mms.TypeSpec{Kind: mms.KindBoolean}, true)
	add("LLN0$RP$Events01$Resv", mms.Boolean(false), mms.TypeSpec{Kind: mms.KindBoolean}, true)
	add("LLN0$RP$Events01$RptID", mms.VisibleString("Events"), mms.TypeSpec{Kind: mms.KindVisibleString, Length: -64}, false)
	add("LLN0$RP$Events01$DatSet", mms.VisibleString("LD/LLN0$Events"), mms.TypeSpec{Kind: mms.KindVisibleString, Length: -64}, false)
	add("LLN0$RP$Events01$ConfRev", mms.Unsigned(1), mms.TypeSpec{Kind: mms.KindUnsigned, Width: 32}, false)
	server, err := mms.NewServer(store, mms.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.AddNamedList(mms.NamedList{Name: mms.DomainName("LD", "LLN0$Events"), Variables: []mms.VariableAccess{mms.Access(mms.DomainName("LD", "GGIO1$ST$SPCSO1$stVal"))}}); err != nil {
		t.Fatal(err)
	}
	server.OnAssociation = func(a *mms.Association) { store.mu.Lock(); store.association = a; store.mu.Unlock() }
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
	client, err := Dial(ctx, listener.Addr().String(), Config{Origin: Origin{Category: 2, ID: []byte("test-station")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client, store
}
func TestControlModelsAndReportsShareOneAssociation(t *testing.T) {
	for _, mode := range []ControlModel{DirectNormal, SBONormal, DirectEnhanced, SBOEnhanced} {
		t.Run(string(rune('0'+mode)), func(t *testing.T) {
			client, _ := dialPeer(t, mode, true)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			block, _ := ObjectName("LD", "RP", "LLN0.Events01")
			sub, err := client.SubscribeURCB(ctx, block, ReportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Operate(ctx, "LD/GGIO1.SPCSO1", data.Boolean(true), ControlOptions{})
			if err != nil || !result.Acknowledged || result.Selected != (mode == SBONormal || mode == SBOEnhanced) || result.Terminated != (mode >= DirectEnhanced) {
				t.Fatal(result, err)
			}
			for i := 0; i < 2; i++ {
				select {
				case event := <-sub.Events():
					if event.Err != nil || len(event.Report.Entries) != 1 {
						t.Fatal(event)
					}
				case <-ctx.Done():
					t.Fatal("report stolen by control dispatcher")
				}
			}
			if err := sub.Stop(ctx); err != nil {
				t.Fatal(err)
			}
			value, err := client.Read(ctx, mms.DomainName("LD", "LLN0$RP$Events01$RptEna"))
			on, _ := value.Bool()
			if err != nil || on {
				t.Fatal(value, err)
			}
		})
	}
}
func TestSlowReportConsumerCannotBlockTermination(t *testing.T) {
	client, _ := dialPeer(t, DirectEnhanced, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	block, _ := ObjectName("LD", "RP", "LLN0.Events01")
	sub, err := client.SubscribeURCB(ctx, block, ReportOptions{Buffer: 1})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Operate(ctx, "LD/GGIO1.SPCSO1", data.Boolean(true), ControlOptions{})
	if err != nil || !result.Terminated {
		t.Fatal(result, err)
	}
	for range sub.Events() {
	}
	if !errors.Is(sub.Err(), ErrReportOverflow) {
		t.Fatal(sub.Err())
	}
	if err := sub.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestMissingTerminationStaysUnconfirmed(t *testing.T) {
	client, _ := dialPeer(t, DirectEnhanced, false)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	result, err := client.Operate(ctx, "LD/GGIO1.SPCSO1", data.Boolean(true), ControlOptions{})
	if !result.Acknowledged || result.Terminated || !errors.Is(err, ErrUnconfirmed) {
		t.Fatal(result, err)
	}
}
func TestConcurrentControlsAreSerialized(t *testing.T) {
	client, _ := dialPeer(t, SBOEnhanced, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := client.Operate(ctx, "LD/GGIO1.SPCSO1", data.Boolean(true), ControlOptions{})
			if err != nil || !result.Terminated {
				t.Error(result, err)
			}
		}()
	}
	wg.Wait()
}

func TestBusyConcludeReturnsWithoutStoppingTheDispatcher(t *testing.T) {
	client, store := dialPeer(t, DirectNormal, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	gate := make(chan struct{})
	started := make(chan struct{})
	store.mu.Lock()
	store.readGate = gate
	store.readStarted = started
	store.mu.Unlock()
	read := make(chan error, 1)
	go func() { _, err := client.Read(ctx, mms.DomainName("LD", "GGIO1$ST$SPCSO1$stVal")); read <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		close(gate)
		t.Fatal(ctx.Err())
	}
	concluded := make(chan error, 1)
	go func() { concluded <- client.Conclude(ctx) }()
	select {
	case err := <-concluded:
		if err == nil || client.Err() != nil {
			close(gate)
			t.Fatal("busy conclude must leave the association open", err, client.Err())
		}
	case <-ctx.Done():
		close(gate)
		t.Fatal("busy conclude waited for the still-running dispatcher")
	}
	close(gate)
	if err := <-read; err != nil {
		t.Fatal(err)
	}
}
