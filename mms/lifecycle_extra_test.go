// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"sync"
	"testing"
	"time"
)

// State callbacks report connecting → connected → closed, and a deliberate
// Close is not reported as a lost connection.
func TestClientStateCallbacks(t *testing.T) {
	_, address := startServer(t, tlsTestStore(t), Config{}, nil)
	var mu sync.Mutex
	var states []State
	var lost []error
	cfg := Config{
		OnStateChanged: func(state State) {
			mu.Lock()
			states = append(states, state)
			mu.Unlock()
		},
		OnConnectionLost: func(err error) { mu.Lock(); lost = append(lost, err); mu.Unlock() },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Dial(ctx, address, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Read(ctx, DomainName("sample", "value01")); err != nil {
		t.Fatal(err)
	}
	client.Close()
	mu.Lock()
	defer mu.Unlock()
	want := []State{StateConnecting, StateConnected, StateClosed}
	if len(states) != len(want) {
		t.Fatalf("states = %v, want %v", states, want)
	}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("states = %v, want %v", states, want)
		}
	}
	if len(lost) != 0 {
		t.Fatalf("a normal close must not report a lost connection: %v", lost)
	}
}

// An abrupt server shutdown is reported as a lost connection.
func TestClientConnectionLost(t *testing.T) {
	server, address := startServer(t, tlsTestStore(t), Config{}, nil)
	lost := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Dial(ctx, address, Config{OnConnectionLost: func(err error) { lost <- err }})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err = server.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-lost:
		if err == nil {
			t.Fatal("lost callback must carry the failure")
		}
	case <-ctx.Done():
		t.Fatal("client did not report the lost connection")
	}
}

// The server bounds concurrent associations and reports accept/close events.
func TestServerMaxConnectionsAndEvents(t *testing.T) {
	var mu sync.Mutex
	events := map[string]int{}
	_, address := startServer(t, tlsTestStore(t), Config{
		MaxConnections: 1,
		OnConnection: func(remote string, connected bool) {
			mu.Lock()
			if connected {
				events["open"]++
			} else {
				events["closed"]++
			}
			mu.Unlock()
		},
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := Dial(ctx, address, Config{})
	if err != nil {
		t.Fatal(err)
	}
	// The limit rejects the second association during accept.
	if _, err = Dial(ctx, address, Config{}); err == nil {
		t.Fatal("the second association must be refused by the connection limit")
	}
	first.Close()
	// Capacity is released once the first association closes.
	deadline := time.Now().Add(3 * time.Second)
	for {
		second, err := Dial(ctx, address, Config{})
		if err == nil {
			second.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("capacity was not released: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if events["open"] < 2 {
		t.Fatalf("accept events missing: %v", events)
	}
}
