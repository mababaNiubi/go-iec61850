// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// testPKI is a throwaway CA with one server and one client leaf certificate.
// Keys and certificates live in memory only; nothing is written to disk.
type testPKI struct {
	ca         *x509.Certificate
	caKey      *ecdsa.PrivateKey
	caPool     *x509.CertPool
	serverCert tls.Certificate
	clientCert tls.Certificate
	otherPool  *x509.CertPool // unrelated CA for negative tests
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "IEDLab Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leaf := func(serial int64, cn string, server bool, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) tls.Certificate {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		usage := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		if server {
			usage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		}
		tpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: cn},
			DNSNames:     []string{"localhost", "ied.test", "server.ied.test"},
			IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  usage,
		}
		der, err := x509.CreateCertificate(rand.Reader, tpl, parent, &key.PublicKey, parentKey)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(9),
		Subject:               pkix.Name{CommonName: "Unrelated CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	otherDER, err := x509.CreateCertificate(rand.Reader, otherTpl, otherTpl, &otherKey.PublicKey, otherKey)
	if err != nil {
		t.Fatal(err)
	}
	other, err := x509.ParseCertificate(otherDER)
	if err != nil {
		t.Fatal(err)
	}
	otherPool := x509.NewCertPool()
	otherPool.AddCert(other)
	return &testPKI{
		ca: ca, caKey: caKey, caPool: pool,
		serverCert: leaf(2, "server.ied.test", true, ca, caKey),
		clientCert: leaf(3, "client.scada", false, ca, caKey),
		otherPool:  otherPool,
	}
}

func tlsTestStore(t *testing.T) *Model {
	t.Helper()
	m := NewModel()
	spec := TypeSpec{Kind: KindInteger, Width: 32}
	if err := m.Add(Variable{Name: DomainName("sample", "value01"), Value: Integer(42), Type: spec, Writable: true}); err != nil {
		t.Fatal(err)
	}
	return m
}

// startTLSServer serves one association over TLS and returns its address.
func startTLSServer(t *testing.T, store Store, policy TLSPolicy) string {
	t.Helper()
	server, err := NewServer(store, Config{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- server.ServeTLS(listener, policy) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		select {
		case err := <-finished:
			if err != nil {
				t.Error(err)
			}
		case <-ctx.Done():
			t.Error("ServeTLS did not stop")
		}
	})
	return listener.Addr().String()
}

// T-SEC-01 (transport half): a TLS-secured association carries the unchanged
// MMS services.
func TestTLSTransportCarriesMMS(t *testing.T) {
	pki := newTestPKI(t)
	address := startTLSServer(t, tlsTestStore(t), TLSPolicy{Certificates: []tls.Certificate{pki.serverCert}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var events []SecurityEvent
	var mu sync.Mutex
	client, err := DialTLS(ctx, address, Config{}, TLSPolicy{
		RootCAs: pki.caPool, ServerName: "server.ied.test",
		OnEvent: func(e SecurityEvent) { mu.Lock(); events = append(events, e); mu.Unlock() },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	value, err := client.Read(ctx, DomainName("sample", "value01"))
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := value.Int(); n != 42 {
		t.Fatalf("read through TLS returned %v", n)
	}
	if err = client.Write(ctx, DomainName("sample", "value01"), Integer(43)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 || events[0].Kind != SecurityHandshakeSucceeded || events[0].PeerCN != "server.ied.test" || !events[0].Verified {
		t.Fatalf("unexpected security events: %+v", events)
	}
}

// A peer with an untrusted chain is refused before any association exists.
func TestTLSTransportRejectsUntrustedPeer(t *testing.T) {
	pki := newTestPKI(t)
	address := startTLSServer(t, tlsTestStore(t), TLSPolicy{Certificates: []tls.Certificate{pki.serverCert}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var rejected []SecurityEvent
	_, err := DialTLS(ctx, address, Config{}, TLSPolicy{
		RootCAs: pki.otherPool, ServerName: "server.ied.test",
		OnEvent: func(e SecurityEvent) { rejected = append(rejected, e) },
	})
	if err == nil {
		t.Fatal("an untrusted server certificate must be refused")
	}
	if len(rejected) != 1 || rejected[0].Kind != SecurityPeerRejected {
		t.Fatalf("expected a peer-rejected event, got %+v", rejected)
	}
	if strings.Contains(rejected[0].Error, "PRIVATE KEY") || strings.Contains(rejected[0].Error, "BEGIN") {
		t.Fatalf("security event leaked key material: %q", rejected[0].Error)
	}
}

// Mutual authentication: the server refuses a client that presents no
// certificate, and accepts one signed by the configured CA.
func TestTLSMutualAuthentication(t *testing.T) {
	pki := newTestPKI(t)
	policy := TLSPolicy{
		Certificates: []tls.Certificate{pki.serverCert},
		ClientCAs:    pki.caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	address := startTLSServer(t, tlsTestStore(t), policy)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := DialTLS(ctx, address, Config{}, TLSPolicy{RootCAs: pki.caPool, ServerName: "server.ied.test"}); err == nil {
		t.Fatal("a client without a certificate must be refused")
	}
	var events []SecurityEvent
	client, err := DialTLS(ctx, address, Config{}, TLSPolicy{
		RootCAs: pki.caPool, ServerName: "server.ied.test",
		Certificates: []tls.Certificate{pki.clientCert},
		OnEvent:      func(e SecurityEvent) { events = append(events, e) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err = client.Read(ctx, DomainName("sample", "value01")); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || !events[0].Mutual || !events[0].Verified {
		t.Fatalf("expected a verified mutual handshake event, got %+v", events)
	}
}

// A plaintext peer and a TLS peer cannot silently interoperate: neither path
// downgrades the transport.
func TestTLSTransportDoesNotDowngrade(t *testing.T) {
	pki := newTestPKI(t)
	address := startTLSServer(t, tlsTestStore(t), TLSPolicy{Certificates: []tls.Certificate{pki.serverCert}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Plaintext client against the TLS server fails.
	if _, err := Dial(ctx, address, Config{}); err == nil {
		t.Fatal("a plaintext association must not succeed against a TLS server")
	}
	// TLS client against a plaintext server fails.
	plain, _ := NewServer(tlsTestStore(t), Config{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- plain.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := plain.Shutdown(shutdownCtx); err != nil {
			t.Error(err)
		}
		<-finished
	}()
	if _, err = DialTLS(ctx, listener.Addr().String(), Config{}, TLSPolicy{RootCAs: pki.caPool, ServerName: "server.ied.test"}); err == nil {
		t.Fatal("a TLS association must not succeed against a plaintext server")
	}
}

func TestTLSPolicyValidation(t *testing.T) {
	pki := newTestPKI(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cases := []struct {
		name   string
		policy TLSPolicy
	}{
		{"insecure without opt-in", TLSPolicy{InsecureSkipVerify: true}},
		{"old TLS version", TLSPolicy{MinVersion: tls.VersionTLS10, Certificates: []tls.Certificate{pki.serverCert}}},
		{"negative timeout", TLSPolicy{HandshakeTimeout: -time.Second}},
	}
	for _, c := range cases {
		if _, err := DialTLS(ctx, "127.0.0.1:9", Config{}, c.policy); err == nil {
			t.Fatalf("%s: expected a configuration error", c.name)
		}
	}
	// Server-side policy needs a certificate and trust anchors for client auth.
	server, err := NewServer(tlsTestStore(t), Config{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err = server.ServeTLS(listener, TLSPolicy{}); err == nil {
		t.Fatal("server policy without a certificate must be refused")
	}
	if err = server.ServeTLS(listener, TLSPolicy{Certificates: []tls.Certificate{pki.serverCert}, ClientAuth: tls.RequireAndVerifyClientCert}); err == nil {
		t.Fatal("client certificate verification without ClientCAs must be refused")
	}
}

// InsecureSkipVerify is reachable only through the explicit opt-in and is
// reported as an insecure peer, never as a verified identity.
func TestTLSInsecureOptIn(t *testing.T) {
	pki := newTestPKI(t)
	address := startTLSServer(t, tlsTestStore(t), TLSPolicy{Certificates: []tls.Certificate{pki.serverCert}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var events []SecurityEvent
	client, err := DialTLS(ctx, address, Config{}, TLSPolicy{
		AllowInsecure: true, InsecureSkipVerify: true,
		OnEvent: func(e SecurityEvent) { events = append(events, e) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err = client.Read(ctx, DomainName("sample", "value01")); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != SecurityHandshakeSucceeded || events[0].Verified {
		t.Fatalf("insecure handshake must not claim a verified peer: %+v", events)
	}
}
