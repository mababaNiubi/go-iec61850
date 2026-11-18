// SPDX-License-Identifier: GPL-3.0-or-later
package scl_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

// T-SEC-01 (authorization half): the remote access policy applies to remote
// associations only, refuses with access-denied, and never affects local
// acquisition.
func TestRemoteAccessPolicy(t *testing.T) {
	d := device(t, fixture(t))
	pos := lookup(t, d, "XCBR1", "ST", "Pos.stVal")
	quality := lookup(t, d, "XCBR1", "ST", "Pos.q")
	deniedRead := 0
	d.SetAccessHandler(func(ctx context.Context, op string, name mms.ObjectName, value mms.Value) error {
		switch {
		case op == scl.AccessRead && name == quality:
			deniedRead++
			return errors.New("policy")
		case op == scl.AccessWrite && strings.HasSuffix(name.Item, "$Oper"):
			return errors.New("policy")
		}
		return nil
	})
	// Local acquisition is not subject to the remote policy.
	if err := d.Update(context.Background(), mms.Assignment{Name: pos, Value: mustPosition(t, scl.Closed)}); err != nil {
		t.Fatal(err)
	}
	server, err := d.NewServer(mms.Config{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		<-finished
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := ied.Dial(ctx, listener.Addr().String(), ied.Config{Origin: ied.Origin{Category: 2, ID: []byte("rbac")}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err = client.Read(ctx, pos); err != nil {
		t.Fatalf("allowed read failed: %v", err)
	}
	if _, err = client.Read(ctx, quality); !errors.Is(err, mms.ObjectAccessDenied) {
		t.Fatalf("denied read must fail with access-denied, got %v", err)
	}
	if deniedRead == 0 {
		t.Fatal("policy was not consulted")
	}
	if _, err = client.Operate(ctx, "BayIEDCTRL/XCBR1.Pos", mms.Boolean(true), ied.ControlOptions{}); !errors.Is(err, mms.ObjectAccessDenied) {
		t.Fatalf("denied control must fail with access-denied, got %v", err)
	}
}

func mustPosition(t *testing.T, p scl.Position) mms.Value {
	t.Helper()
	v, err := p.MMSValue()
	if err != nil {
		t.Fatal(err)
	}
	return v
}
