// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"errors"
	"testing"
	"time"
)

// T-SEC-01 (association half): ACSE simple authentication. The server policy is
// explicit: an installed authenticator sees every association, and without one
// credentials are refused rather than ignored.
func TestACSESimpleAuthentication(t *testing.T) {
	var seen []string
	server, address := startServer(t, tlsTestStore(t), Config{
		Authenticator: func(mechanism ACSEAuthMechanism, value []byte) error {
			seen = append(seen, string(mechanism)+":"+string(value))
			if mechanism != ACSEAuthPassword || string(value) != "secret" {
				return errors.New("bad credentials")
			}
			return nil
		},
	}, nil)
	_ = server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, address, Config{Password: []byte("secret")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Read(ctx, DomainName("sample", "value01")); err != nil {
		t.Fatal(err)
	}
	client.Close()

	// Wrong password: the authenticator refuses and the association fails.
	if _, err = Dial(ctx, address, Config{Password: []byte("wrong")}); err == nil {
		t.Fatal("a refused password must not establish an association")
	}
	// No credentials: the authenticator still decides, and refuses.
	if _, err = Dial(ctx, address, Config{}); err == nil {
		t.Fatal("missing credentials must be refused by a password policy")
	}
	if len(seen) != 3 {
		t.Fatalf("authenticator must see every association, saw %v", seen)
	}
}

// A server with no authenticator accepts plain associations and refuses any
// association that presents credentials.
func TestACSEAuthNeverSilentlyIgnored(t *testing.T) {
	_, address := startServer(t, tlsTestStore(t), Config{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Dial(ctx, address, Config{})
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	if _, err = Dial(ctx, address, Config{Password: []byte("secret")}); err == nil {
		t.Fatal("credentials presented to a server without an authenticator must be refused")
	}
}
