// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
)

// countingStore counts attribute requests so tests can prove a binding
// reuses metadata instead of re-requesting one type per member on every poll.
type countingStore struct {
	*mms.Model
	typeCalls int32
}

func (s *countingStore) Type(ctx context.Context, n mms.ObjectName) (mms.TypeSpec, error) {
	atomic.AddInt32(&s.typeCalls, 1)
	return s.Model.Type(ctx, n)
}

// bindPeer serves one counting store with two clients sharing the listener.
func bindPeer(t *testing.T) (*Client, *Client, *countingStore) {
	t.Helper()
	model := mms.NewModel()
	store := &countingStore{Model: model}
	add := func(item string, value mms.Value, spec mms.TypeSpec, writable bool) {
		t.Helper()
		if err := model.Add(mms.Variable{Name: mms.DomainName("LD", item), Value: value, Type: spec, Writable: writable}); err != nil {
			t.Fatal(err)
		}
	}
	add("GGIO1$ST$SPCSO1$stVal", mms.Boolean(false), mms.TypeSpec{Kind: mms.KindBoolean}, false)
	add("GGIO1$ST$Ind1$stVal", mms.Boolean(true), mms.TypeSpec{Kind: mms.KindBoolean}, false)
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
	return dial(), dial(), store
}

// T-DS-01: repeated bound reads reuse metadata; a one-shot ReadDataSet pays
// the type requests again on every call.
func TestBindingReusesMetadata(t *testing.T) {
	reader, writer, store := bindPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list := mms.DomainName("LD", "LLN0$Dyn")
	if err := writer.raw.DefineNamedList(ctx, list, []mms.VariableAccess{mms.Access(mms.DomainName("LD", "GGIO1$ST$SPCSO1$stVal"))}); err != nil {
		t.Fatal(err)
	}
	binding, err := reader.BindDataSet(ctx, list, BindOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	baseline := atomic.LoadInt32(&store.typeCalls)
	for i := 0; i < 3; i++ {
		snapshot, err := binding.Read(ctx)
		if err != nil || len(snapshot.Entries) != 1 {
			t.Fatal(snapshot, err)
		}
	}
	if n := atomic.LoadInt32(&store.typeCalls); n != baseline {
		t.Fatalf("bound reads issued %d extra attribute requests", n-baseline)
	}
	for i := 0; i < 2; i++ {
		if _, err := reader.ReadDataSet(ctx, list); err != nil {
			t.Fatal(err)
		}
	}
	if n := atomic.LoadInt32(&store.typeCalls); n != baseline+2 {
		t.Fatalf("one-shot reads should pay per-call metadata, got %d extra", n-baseline)
	}
}

func TestBindingRefreshPublishesNewGeneration(t *testing.T) {
	reader, writer, _ := bindPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list := mms.DomainName("LD", "LLN0$Dyn")
	if err := writer.raw.DefineNamedList(ctx, list, []mms.VariableAccess{mms.Access(mms.DomainName("LD", "GGIO1$ST$SPCSO1$stVal"))}); err != nil {
		t.Fatal(err)
	}
	binding, err := reader.BindDataSet(ctx, list, BindOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	before := binding.Info()
	// Replace the declaration under the binding.
	if _, err = writer.raw.DeleteNamedList(ctx, list); err != nil {
		t.Fatal(err)
	}
	if err = writer.raw.DefineNamedList(ctx, list, []mms.VariableAccess{mms.Access(mms.DomainName("LD", "GGIO1$ST$Ind1$stVal"))}); err != nil {
		t.Fatal(err)
	}
	after, err := binding.Refresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.GenerationID == before.GenerationID {
		t.Fatal("refresh must publish a new generation after a member change")
	}
	snapshot, err := binding.Read(ctx)
	if err != nil || len(snapshot.Entries) != 1 || snapshot.Entries[0].Variable.Name.Item != "GGIO1$ST$Ind1$stVal" {
		t.Fatalf("bound read after refresh: %+v err=%v", snapshot, err)
	}
	// An unchanged refresh keeps the generation stable.
	again, err := binding.Refresh(ctx)
	if err != nil || again.GenerationID != after.GenerationID {
		t.Fatalf("unchanged refresh must not bump the generation: %+v err=%v", again, err)
	}
}

func TestBindingEachReadDetectsChangeAndGoesStale(t *testing.T) {
	reader, writer, store, gate := generationPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list := mms.DomainName("LD", "LLN0$Dyn")
	if err := writer.raw.DefineNamedList(ctx, list, []mms.VariableAccess{mms.Access(mms.DomainName("LD", "GGIO1$ST$SPCSO1$stVal"))}); err != nil {
		t.Fatal(err)
	}
	binding, err := reader.BindDataSet(ctx, list, BindOptions{VerifyEachRead: true})
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	result := make(chan error, 1)
	go func() {
		_, err := binding.Read(ctx)
		result <- err
	}()
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
	if err := <-result; !errors.Is(err, ErrConfigurationChanged) {
		t.Fatalf("expected a typed change, got %v", err)
	}
	// A stale binding fails fast instead of reading under the old generation.
	if _, err := binding.Read(ctx); !errors.Is(err, ErrConfigurationChanged) {
		t.Fatalf("stale binding must refuse reads, got %v", err)
	}
	if _, err := binding.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, err := binding.Read(ctx)
	if err != nil || !snapshot.Verified {
		t.Fatalf("read after refresh: %+v err=%v", snapshot, err)
	}
}

func TestBindingCloseRefusesReads(t *testing.T) {
	reader, writer, _ := bindPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list := mms.DomainName("LD", "LLN0$Dyn")
	if err := writer.raw.DefineNamedList(ctx, list, []mms.VariableAccess{mms.Access(mms.DomainName("LD", "GGIO1$ST$SPCSO1$stVal"))}); err != nil {
		t.Fatal(err)
	}
	binding, err := reader.BindDataSet(ctx, list, BindOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := binding.Read(ctx); !errors.Is(err, mms.ErrClosed) {
		t.Fatalf("closed binding must refuse reads, got %v", err)
	}
	if _, err := binding.Refresh(ctx); !errors.Is(err, mms.ErrClosed) {
		t.Fatalf("closed binding must refuse refresh, got %v", err)
	}
	// The shared client and the remote list stay usable.
	if _, err := reader.ReadDataSet(ctx, list); err != nil {
		t.Fatal(err)
	}
}
