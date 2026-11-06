// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
)

func testModel(t *testing.T) *Model {
	t.Helper()
	m := NewModel()
	i32 := TypeSpec{Kind: KindInteger, Width: 32}
	b := TypeSpec{Kind: KindBoolean}
	vars := []Variable{
		{Name: DomainName("sample", "bool"), Type: b, Value: Boolean(true), Writable: true},
		{Name: DomainName("sample", "int"), Type: i32, Value: Integer(42), Writable: true},
		{Name: DomainName("sample", "uint"), Type: TypeSpec{Kind: KindUnsigned, Width: 32}, Value: Unsigned(123), Writable: true},
		{Name: DomainName("sample", "float"), Type: TypeSpec{Kind: KindFloat, Width: 32, ExponentWidth: 8}, Value: Float32(1.25), Writable: true},
		{Name: DomainName("sample", "text"), Type: TypeSpec{Kind: KindVisibleString, Length: -64}, Value: VisibleString("hello"), Writable: true},
		{Name: DomainName("sample", "array"), Type: TypeSpec{Kind: KindArray, Length: 3, Element: &i32}, Value: Array(Integer(1), Integer(2), Integer(3)), Writable: true},
		{Name: DomainName("sample", "struct"), Type: TypeSpec{Kind: KindStructure, Components: []Component{{Name: "flag", Type: b}, {Name: "count", Type: i32}}}, Value: Structure(Boolean(true), Integer(7)), Writable: true},
		{Name: DomainName("sample", "readonly"), Type: i32, Value: Integer(9)},
	}
	for _, v := range vars {
		if err := m.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 40; i++ {
		if err := m.Add(Variable{Name: DomainName("sample", fmt.Sprintf("item%03d", i)), Type: i32, Value: Integer(int64(i)), Writable: true}); err != nil {
			t.Fatal(err)
		}
	}
	return m
}
func startServer(t *testing.T, store Store, cfg Config, configure func(*Server)) (*Server, string) {
	t.Helper()
	s, err := NewServer(store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if configure != nil {
		configure(s)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- s.Serve(l) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		select {
		case err := <-finished:
			if err != nil {
				t.Error(err)
			}
		case <-ctx.Done():
			t.Error("Serve did not stop")
		}
	})
	return s, l.Addr().String()
}
func dialTest(t *testing.T, address string, cfg Config) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := Dial(ctx, address, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func requireInteger(t *testing.T, v Value, want int64) {
	t.Helper()
	got, err := v.Int()
	if err != nil || got != want {
		t.Fatalf("integer got %v (%v), want %d", got, err, want)
	}
}

func TestMMSCore(t *testing.T) {
	_, addr := startServer(t, testModel(t), Config{}, nil)
	c := dialTest(t, addr, Config{MaxPDUSize: 256})
	ctx := context.Background()
	if p := c.Parameters(); p.MaxPDUSize != 256 || p.MaxOutstandingCalling != 5 || !p.Supports(4) || p.Supports(72) {
		t.Fatalf("negotiation %+v", p)
	}
	id, err := c.Identify(ctx)
	if err != nil || id.Vendor != "Go MMS" {
		t.Fatalf("identity %+v %v", id, err)
	}
	status, err := c.Status(ctx)
	if err != nil || status != (Status{}) {
		t.Fatalf("status %+v %v", status, err)
	}
	domains, err := c.Names(ctx, DomainObject, VMDScope, "")
	if err != nil || len(domains) != 1 || domains[0] != "sample" {
		t.Fatalf("domains %v %v", domains, err)
	}
	page, err := c.GetNameList(ctx, NamedVariable, DomainScope, "sample", "")
	if err != nil || !page.MoreFollows {
		t.Fatalf("paging %v %v", page, err)
	}
	names, err := c.Names(ctx, NamedVariable, DomainScope, "sample")
	if err != nil || len(names) != 50 {
		t.Fatalf("directory count=%d %v", len(names), err)
	}
	typeSpec, err := c.GetVariableAccessAttributes(ctx, DomainName("sample", "struct"))
	if err != nil || len(typeSpec.Components) != 2 || typeSpec.Components[1].Name != "count" {
		t.Fatalf("type %+v %v", typeSpec, err)
	}
	for _, name := range []string{"bool", "int", "uint", "float", "text", "array", "struct"} {
		if _, err := c.Read(ctx, DomainName("sample", name)); err != nil {
			t.Errorf("read %s: %v", name, err)
		}
	}
	items, err := c.ReadMultiple(ctx, Access(DomainName("sample", "int")), Access(DomainName("sample", "missing")))
	if err != nil || len(items) != 2 || !errors.Is(items[1].Error, ObjectNonExistent) {
		t.Fatalf("partial read %v %v", items, err)
	}
	requireInteger(t, items[0].Value, 42)
	if err := c.Write(ctx, DomainName("sample", "int"), Integer(99)); err != nil {
		t.Fatal(err)
	}
	v, err := c.Read(ctx, DomainName("sample", "int"))
	if err != nil {
		t.Fatal(err)
	}
	requireInteger(t, v, 99)
	if err := c.Write(ctx, DomainName("sample", "readonly"), Integer(2)); !errors.Is(err, ObjectAccessDenied) {
		t.Fatalf("readonly %v", err)
	}
	if err := c.Write(ctx, DomainName("sample", "int"), Boolean(false)); !errors.Is(err, TypeInconsistent) {
		t.Fatalf("type mismatch %v", err)
	}
	if err := c.Write(ctx, DomainName("sample", "int"), Integer(1<<40)); !errors.Is(err, TypeInconsistent) {
		t.Fatalf("width %v", err)
	}
	results, err := c.WriteMultiple(ctx, []VariableAccess{Access(DomainName("sample", "int")), Access(DomainName("sample", "readonly"))}, []Value{Integer(100), Integer(1)})
	if err != nil || len(results) != 2 || results[0] != nil || !errors.Is(results[1], ObjectAccessDenied) {
		t.Fatalf("partial write %v %v", results, err)
	}
	if _, err := c.GetVariableAccessAttributes(ctx, DomainName("sample", "missing")); err == nil {
		t.Fatal("missing attributes succeeded")
	}
	if _, err := c.call(ctx, ber.Encode(0x87), 0x87); err == nil {
		t.Fatal("unsupported service succeeded")
	} else {
		var reject *RejectError
		if !errors.As(err, &reject) || reject.Reason != 1 {
			t.Fatalf("reject %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.Conclude(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestAlternateAccessAndLists(t *testing.T) {
	s, addr := startServer(t, testModel(t), Config{}, nil)
	c := dialTest(t, addr, Config{})
	ctx := context.Background()
	v, err := c.ReadAccess(ctx, Element(DomainName("sample", "array"), 1))
	if err != nil {
		t.Fatal(err)
	}
	requireInteger(t, v, 2)
	v, err = c.ReadAccess(ctx, Member(DomainName("sample", "struct"), "count"))
	if err != nil {
		t.Fatal(err)
	}
	requireInteger(t, v, 7)
	if err = c.WriteAccess(ctx, Element(DomainName("sample", "array"), 1), Integer(12)); err != nil {
		t.Fatal(err)
	}
	if err = c.Write(ctx, DomainName("sample", "struct$count"), Integer(14)); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ReadAccess(ctx, Element(DomainName("sample", "array"), 3)); !errors.Is(err, InvalidAddress) {
		t.Fatalf("index bound %v", err)
	}
	variables := []VariableAccess{Access(DomainName("sample", "int")), Member(DomainName("sample", "struct"), "count")}
	for _, name := range []ObjectName{DomainName("sample", "dynamic"), AssociationName("private"), VMDName("global")} {
		if err = c.DefineNamedList(ctx, name, variables); err != nil {
			t.Fatal(err)
		}
		attrs, err := c.GetNamedListAttributes(ctx, name)
		if err != nil || !attrs.Deletable || len(attrs.Variables) != 2 {
			t.Fatalf("list attrs %+v %v", attrs, err)
		}
		r, err := c.ReadNamedList(ctx, name)
		if err != nil || len(r) != 2 {
			t.Fatalf("list read %v %v", r, err)
		}
		requireInteger(t, r[1].Value, 14)
		if err = c.DefineNamedList(ctx, name, variables); err == nil {
			t.Fatal("duplicate list accepted")
		}
		if name.Scope == AssociationScope {
			other := dialTest(t, addr, Config{})
			if _, err = other.GetNamedListAttributes(ctx, name); err == nil {
				t.Fatal("association list leaked")
			}
		}
		deleted, err := c.DeleteNamedList(ctx, name)
		if err != nil || deleted != (DeleteResult{1, 1}) {
			t.Fatalf("delete %+v %v", deleted, err)
		}
	}
	static := DomainName("sample", "static")
	if err = s.AddNamedList(NamedList{Name: static, Variables: variables}); err != nil {
		t.Fatal(err)
	}
	deleted, err := c.DeleteNamedList(ctx, static)
	if err != nil || deleted != (DeleteResult{1, 0}) {
		t.Fatalf("static delete %+v %v", deleted, err)
	}
}
func TestInformationReports(t *testing.T) {
	connected := make(chan *Association, 1)
	_, addr := startServer(t, testModel(t), Config{}, func(s *Server) { s.OnAssociation = func(a *Association) { connected <- a } })
	c := dialTest(t, addr, Config{})
	a := <-connected
	ctx := context.Background()
	r := InformationReport{Variables: []VariableAccess{Access(DomainName("sample", "int"))}, Results: []ReadResult{{Value: Integer(77)}}}
	if err := a.SendReport(ctx, r); err != nil {
		t.Fatal(err)
	}
	select {
	case report := <-c.Reports():
		if len(report.Results) != 1 || len(report.Variables) != 1 {
			t.Fatalf("report %+v", report)
		}
		requireInteger(t, report.Results[0].Value, 77)
	case <-time.After(time.Second):
		t.Fatal("report not delivered")
	}
	if _, err := c.Read(ctx, DomainName("sample", "int")); err != nil {
		t.Fatal(err)
	}
	name := VMDName("report")
	if err := a.SendReport(ctx, InformationReport{List: &name, Results: []ReadResult{{Value: Boolean(true)}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case report := <-c.Reports():
		if report.List == nil || *report.List != name {
			t.Fatalf("named report %+v", report)
		}
	case <-time.After(time.Second):
		t.Fatal("named report not delivered")
	}
}

type delayedStore struct {
	Store
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *delayedStore) Read(ctx context.Context, a VariableAccess) (Value, error) {
	if a.Name.Item == "item000" {
		s.once.Do(func() { close(s.entered) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return Value{}, ctx.Err()
		}
	}
	return s.Store.Read(ctx, a)
}
func TestConcurrentCallsAndLateResponse(t *testing.T) {
	store := &delayedStore{Store: testModel(t), entered: make(chan struct{}), release: make(chan struct{})}
	_, addr := startServer(t, store, Config{}, nil)
	c := dialTest(t, addr, Config{})
	ctx := context.Background()
	slowCtx, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { _, err := c.Read(slowCtx, DomainName("sample", "item000")); result <- err }()
	<-store.entered
	v, err := c.Read(ctx, DomainName("sample", "int"))
	if err != nil {
		t.Fatal(err)
	}
	requireInteger(t, v, 42)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel %v", err)
	}
	close(store.release)
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("cancelled sent request did not close association")
	}
	c = dialTest(t, addr, Config{})
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := c.Read(ctx, DomainName("sample", fmt.Sprintf("item%03d", i)))
			if err != nil {
				t.Error(err)
				return
			}
			got, err := v.Int()
			if err != nil || got != int64(i) {
				t.Errorf("response mismatch %d %d %v", i, got, err)
			}
		}(i)
	}
	wg.Wait()
}
func TestModelDefensiveTypes(t *testing.T) {
	m := testModel(t)
	ctx := context.Background()
	n := DomainName("sample", "struct")
	spec, err := m.Type(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	spec.Components[0].Name = "changed"
	spec2, _ := m.Type(ctx, n)
	if spec2.Components[0].Name != "flag" {
		t.Fatal("mutable type escaped")
	}
}
func FuzzEnvelope(f *testing.F) {
	f.Add([]byte{0xa0, 5, 2, 1, 1, 0x82, 0})
	f.Add([]byte{0x8b, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		decodeEnvelope(data)
	})
}
