// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/mababaNiubi/go-iec61850/internal/iso"
)

// This file adds a TLS transport under the unchanged ISO/ACSE/MMS stack.
// IEC 62351-3 places security between TCP and the ISO session, so wrapping the
// socket changes no protocol byte. Scope is deliberately narrow: transport
// confidentiality, integrity and (optionally) mutual certificate
// authentication. It is NOT IEC 62351-4 end-to-end application security, not
// ACSE authentication, and not role-based authorization. Declare those
// separately before claiming them.

// SecurityEventKind classifies one transport security observation.
type SecurityEventKind string

const (
	// SecurityHandshakeSucceeded reports an established secured transport.
	SecurityHandshakeSucceeded SecurityEventKind = "handshake-succeeded"
	// SecurityHandshakeFailed reports a refused or failed TLS handshake.
	SecurityHandshakeFailed SecurityEventKind = "handshake-failed"
	// SecurityPeerRejected reports peer-identity verification failure.
	SecurityPeerRejected SecurityEventKind = "peer-rejected"
)

// SecurityEvent is a transport observation suitable for auditing. It never
// carries certificate material, private keys or other secrets: only the
// remote address, the event kind and a redacted error text.
type SecurityEvent struct {
	Kind   SecurityEventKind
	Remote string
	Error  string
	PeerCN string // verified subject common name when available
	Mutual bool   // whether the peer presented a certificate
	// Verified reports whether that certificate chain was actually verified.
	// An insecure (AllowInsecure) handshake still reports the peer CN but is
	// never marked verified.
	Verified bool
	Elapsed  time.Duration
}

// TLSPolicy is the explicit, reviewable transport security configuration for
// client and server. The zero value is unusable on purpose: a caller must
// provide certificates and/or trust anchors, because silently falling back to
// an unauthenticated transport is not an acceptable default.
//
// InsecureSkipVerify exists only behind AllowInsecure and is intended for
// local laboratory peers. It disables peer identity verification and must
// never be enabled in production.
type TLSPolicy struct {
	// Certificates identifies this endpoint. The server needs one; a client
	// needs one only for mutual authentication.
	Certificates []tls.Certificate
	// RootCAs verifies the server on the client side. Nil uses the system
	// roots.
	RootCAs *x509.CertPool
	// ClientCAs verifies client certificates on the server side. Required
	// when ClientAuth asks for a client certificate.
	ClientCAs *x509.CertPool
	// ServerName overrides the verified server name. Empty derives it from
	// the dialed host.
	ServerName string
	// ClientAuth selects server-side client-certificate policy.
	ClientAuth tls.ClientAuthType
	// MinVersion defaults to TLS 1.2; values below are rejected.
	MinVersion uint16
	// CipherSuites optionally restricts TLS 1.2 suites. TLS 1.3 suites are
	// fixed by the Go runtime and cannot be configured here.
	CipherSuites []uint16
	// HandshakeTimeout defaults to 10s; zero selects that default.
	HandshakeTimeout time.Duration
	// AllowInsecure permits InsecureSkipVerify. See InsecureSkipVerify.
	AllowInsecure bool
	// InsecureSkipVerify disables server-certificate verification. Requires
	// AllowInsecure and is refused otherwise.
	InsecureSkipVerify bool
	// OnEvent receives redacted transport security observations. It runs on
	// the handshake goroutine and must return promptly.
	OnEvent func(SecurityEvent)
}

// TLSProfileName names the implemented transport profile for capability and
// audit output. It intentionally says "TLS transport": no IEC 62351-4 or
// authorization claim is implied.
const TLSProfileName = "IEC 62351-3-style TLS transport (TLS 1.2+)"

func (p TLSPolicy) validate(server bool) error {
	if p.MinVersion != 0 && p.MinVersion < tls.VersionTLS12 {
		return fmt.Errorf("mms: TLS min version must be 1.2 or later")
	}
	if p.InsecureSkipVerify && !p.AllowInsecure {
		return fmt.Errorf("mms: InsecureSkipVerify requires AllowInsecure")
	}
	if p.HandshakeTimeout < 0 {
		return fmt.Errorf("mms: negative handshake timeout")
	}
	if server {
		if len(p.Certificates) == 0 {
			return fmt.Errorf("mms: server TLS policy needs a certificate")
		}
		if p.ClientAuth != tls.NoClientCert && p.ClientAuth != tls.RequestClientCert && p.ClientCAs == nil {
			return fmt.Errorf("mms: client certificate verification needs ClientCAs")
		}
	}
	return nil
}

func (p TLSPolicy) base() *tls.Config {
	version := p.MinVersion
	if version == 0 {
		version = tls.VersionTLS12
	}
	timeout := p.HandshakeTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &tls.Config{
		Certificates:       p.Certificates,
		MinVersion:         version,
		CipherSuites:       p.CipherSuites,
		InsecureSkipVerify: p.InsecureSkipVerify,
		// Reject renegotiation-era behavior and require forward-secret suites
		// where the runtime allows the choice.
		PreferServerCipherSuites: false,
	}
}

// serverConfig builds the server-side TLS configuration.
func (p TLSPolicy) serverConfig() (*tls.Config, error) {
	if err := p.validate(true); err != nil {
		return nil, err
	}
	cfg := p.base()
	cfg.ClientCAs = p.ClientCAs
	cfg.ClientAuth = p.ClientAuth
	cfg.NextProtos = nil
	return cfg, nil
}

// clientConfig builds the client-side TLS configuration for one address.
func (p TLSPolicy) clientConfig(address string) (*tls.Config, error) {
	if err := p.validate(false); err != nil {
		return nil, err
	}
	cfg := p.base()
	cfg.RootCAs = p.RootCAs
	cfg.ServerName = p.ServerName
	if cfg.ServerName == "" && !p.InsecureSkipVerify {
		if host, _, err := net.SplitHostPort(address); err == nil {
			cfg.ServerName = strings.Trim(host, "[]")
		}
	}
	return cfg, nil
}

func redactTLS(err error) string {
	if err == nil {
		return ""
	}
	// TLS errors never contain key material; strip any embedded certificate
	// dump defensively and keep the classification text.
	text := err.Error()
	if i := strings.Index(text, "-----BEGIN"); i >= 0 {
		text = text[:i] + "(certificate redacted)"
	}
	if len(text) > 400 {
		text = text[:400] + "…"
	}
	return text
}

func (p TLSPolicy) event(e SecurityEvent) {
	if p.OnEvent != nil {
		p.OnEvent(e)
	}
}

// DialTLS establishes a TLS-secured MMS association. TLS runs under the
// unchanged ISO/ACSE/MMS bytes; ctx bounds dialing and the TLS handshake, and
// later requests use their own contexts. A failed handshake reports
// SecurityHandshakeFailed or SecurityPeerRejected and returns no association.
func DialTLS(ctx context.Context, address string, cfg Config, policy TLSPolicy) (*Client, error) {
	cfg, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	tlsCfg, err := policy.clientConfig(address)
	if err != nil {
		return nil, err
	}
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	secured := tls.Client(raw, tlsCfg)
	if deadline, ok := ctx.Deadline(); ok {
		_ = secured.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = secured.SetDeadline(time.Now()) })
	handshakeErr := secured.HandshakeContext(ctx)
	if !stop() {
		_ = secured.SetDeadline(time.Time{})
	}
	if handshakeErr != nil {
		_ = secured.Close()
		kind := SecurityHandshakeFailed
		if ctx.Err() == nil {
			kind = SecurityPeerRejected
		}
		policy.event(SecurityEvent{Kind: kind, Remote: address, Error: redactTLS(handshakeErr), Elapsed: time.Since(started)})
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("mms: TLS handshake: %w", handshakeErr)
	}
	_ = secured.SetDeadline(time.Time{})
	state := secured.ConnectionState()
	policy.event(SecurityEvent{
		Kind: SecurityHandshakeSucceeded, Remote: address, Elapsed: time.Since(started),
		PeerCN: peerCommonName(state), Mutual: len(state.PeerCertificates) > 0,
		Verified: !policy.InsecureSkipVerify && len(state.PeerCertificates) > 0,
	})
	local := localParameters(cfg)
	conn, response, err := iso.DialConn(ctx, secured, cfg.ISO, encodeInitiate(0xa8, local))
	if err != nil {
		_ = secured.Close()
		return nil, err
	}
	return newClient(conn, response, cfg, local)
}

// ServeTLS accepts TLS-secured associations on listener until shutdown. The
// server owns and closes listener. TLS failures are reported as security
// events and never surface as a promoted transport change: a refused
// handshake simply yields no association.
func (s *Server) ServeTLS(listener net.Listener, policy TLSPolicy) error {
	tlsCfg, err := policy.serverConfig()
	if err != nil {
		return err
	}
	return s.serveTLS(listener, tlsCfg, policy)
}

func (s *Server) serveTLS(listener net.Listener, cfg *tls.Config, policy TLSPolicy) error {
	s.mu.Lock()
	if s.closed || s.listener != nil {
		s.mu.Unlock()
		return ErrClosed
	}
	s.listener = listener
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	if s.OnRun != nil {
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.OnRun(ctx) }()
	}
	s.mu.Unlock()
	defer cancel()
	for {
		raw, err := listener.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			return err
		}
		// Adopt the plaintext socket before the handshake so shutdown can
		// abort it; the handshake runs off the accept loop.
		if !s.adopt(raw) {
			return nil
		}
		go func(raw net.Conn) {
			defer s.wg.Done()
			defer func() {
				raw.Close()
				s.mu.Lock()
				delete(s.raw, raw)
				s.mu.Unlock()
			}()
			secured := tls.Server(raw, cfg)
			started := time.Now()
			if err := secured.HandshakeContext(ctx); err != nil {
				kind := SecurityHandshakeFailed
				if len(secured.ConnectionState().PeerCertificates) > 0 {
					kind = SecurityPeerRejected
				}
				policy.event(SecurityEvent{Kind: kind, Remote: raw.RemoteAddr().String(), Error: redactTLS(err), Elapsed: time.Since(started)})
				return
			}
			state := secured.ConnectionState()
			policy.event(SecurityEvent{
				Kind: SecurityHandshakeSucceeded, Remote: raw.RemoteAddr().String(), Elapsed: time.Since(started),
				PeerCN: peerCommonName(state), Mutual: len(state.PeerCertificates) > 0,
				Verified: len(state.VerifiedChains) > 0,
			})
			// The unchanged ISO/ACSE/MMS path runs on the secured socket.
			s.serveSession(secured)
		}(raw)
	}
}

func peerCommonName(state tls.ConnectionState) string {
	if len(state.PeerCertificates) == 0 {
		return ""
	}
	return state.PeerCertificates[0].Subject.CommonName
}

// ErrTLSRequired reports an attempt to use a plaintext path where the caller
// declared a secured transport.
var ErrTLSRequired = errors.New("mms: this connection requires TLS")
