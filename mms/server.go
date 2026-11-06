// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
	"github.com/mababaNiubi/go-iec61850/internal/iso"
)

// Server serves a concurrency-safe Store. Configure exported fields before Serve.
type Server struct {
	Store         Store
	Identity      Identity
	Status        Status
	OnAssociation func(*Association)
	cfg           Config
	mu            sync.Mutex
	listener      net.Listener
	closed        bool
	raw           map[net.Conn]struct{}
	lists         map[ObjectName]NamedList
	wg            sync.WaitGroup
}

// Association identifies a server-side association. Association-scoped lists
// are private to it and disappear when the association closes.
type Association struct {
	server        *Server
	conn          *iso.Conn
	params        Parameters
	RemoteAddress string
	ctx           context.Context
	sendToken     chan struct{}
	mu            sync.Mutex
	lists         map[ObjectName]NamedList
	active        map[uint32]struct{}
	concluded     bool
}

func NewServer(store Store, cfg Config) (*Server, error) {
	if store == nil {
		return nil, fmt.Errorf("mms: nil Store")
	}
	cfg, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	return &Server{Store: store, cfg: cfg, Identity: Identity{"Go MMS", "Native Go MMS", "0.1.0"}, raw: make(map[net.Conn]struct{}), lists: make(map[ObjectName]NamedList)}, nil
}
func (s *Server) Serve(listener net.Listener) error {
	s.mu.Lock()
	if s.closed || s.listener != nil {
		s.mu.Unlock()
		return ErrClosed
	}
	s.listener = listener
	s.mu.Unlock()
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
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			raw.Close()
			return nil
		}
		s.raw[raw] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go s.serveConnection(raw)
	}
}
func (s *Server) ListenAndServe(address string) error {
	l, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer l.Close()
	return s.Serve(l)
}
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.listener != nil {
		s.listener.Close()
	}
	for raw := range s.raw {
		raw.Close()
	}
	return nil
}

// Shutdown closes network connections and waits for handlers to finish.
func (s *Server) Shutdown(ctx context.Context) error {
	s.Close()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (a *Association) Parameters() Parameters   { return a.params }
func (a *Association) Context() context.Context { return a.ctx }
func (a *Association) send(data []byte) error {
	ctx, cancel := context.WithTimeout(a.ctx, a.server.cfg.RequestTimeout)
	defer cancel()
	if len(data) > a.params.MaxPDUSize {
		return fmt.Errorf("mms: response exceeds negotiated PDU size")
	}
	return sendPDU(ctx, a.conn, a.sendToken, data)
}
func (s *Server) serveConnection(raw net.Conn) {
	defer s.wg.Done()
	defer func() { raw.Close(); s.mu.Lock(); delete(s.raw, raw); s.mu.Unlock() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local := localParameters(s.cfg)
	params := local
	conn, err := iso.Accept(ctx, raw, s.cfg.ISO, func(request []byte) ([]byte, error) {
		peer, err := decodeInitiate(request, 0xa8)
		if err != nil {
			return nil, err
		}
		params = negotiate(local, peer)
		return encodeInitiate(0xa9, params), nil
	})
	if err != nil {
		return
	}
	defer conn.Close()
	a := &Association{server: s, conn: conn, params: params, RemoteAddress: raw.RemoteAddr().String(), ctx: ctx, sendToken: make(chan struct{}, 1), lists: make(map[ObjectName]NamedList), active: make(map[uint32]struct{})}
	var workers sync.WaitGroup
	defer func() { cancel(); conn.Close(); workers.Wait() }()
	if s.OnAssociation != nil {
		workers.Add(1)
		go func() { defer workers.Done(); s.OnAssociation(a) }()
	}
	for {
		data, err := conn.ReadPDU()
		if err != nil {
			return
		}
		if len(data) > params.MaxPDUSize {
			return
		}
		msg, err := decodeEnvelope(data)
		if err != nil {
			a.send(reject(0, 1, 4))
			return
		}
		if msg.tag == 0x8b {
			a.mu.Lock()
			busy := len(a.active) > 0
			if a.concluded {
				a.mu.Unlock()
				return
			}
			if busy {
				a.mu.Unlock()
				a.send(ber.Encode(0xad, ber.Encode(0xa0, ber.Uint(0x84, 2))))
				continue
			}
			a.concluded = true
			err = a.send([]byte{0x8c, 0})
			a.mu.Unlock()
			if err != nil {
				return
			}
			continue
		}
		if msg.tag != 0xa0 {
			a.send(reject(msg.id, 5, 0))
			continue
		}
		a.mu.Lock()
		if a.concluded {
			a.mu.Unlock()
			return
		}
		_, duplicate := a.active[msg.id]
		full := len(a.active) >= params.MaxOutstandingCalling
		if !duplicate && !full {
			a.active[msg.id] = struct{}{}
		}
		a.mu.Unlock()
		if duplicate {
			a.send(reject(msg.id, 1, 0))
			continue
		}
		if full {
			a.send(serviceFailure(msg.id, 3, 0))
			continue
		}
		workers.Add(1)
		go func(m envelope) {
			defer workers.Done()
			response := a.dispatch(m)
			// Retire the request while serializing the response and admission
			// state, before the peer can reuse a window slot or conclude.
			a.mu.Lock()
			delete(a.active, m.id)
			err := a.send(response)
			a.mu.Unlock()
			if err != nil {
				conn.Close()
			}
		}(msg)
	}
}
func (a *Association) failure(id uint32, err error) []byte {
	var se *ServiceError
	if errors.As(err, &se) {
		return serviceFailure(id, se.Class, se.Code)
	}
	if errors.Is(err, ErrProtocol) {
		return reject(id, 1, 4)
	}
	var da DataAccessError
	if errors.As(err, &da) {
		if da == ObjectNonExistent || da == ObjectUndefined {
			return serviceFailure(id, 7, 2)
		}
		return serviceFailure(id, 7, 3)
	}
	return serviceFailure(id, 3, 0)
}
func dataError(err error) DataAccessError {
	var e DataAccessError
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return TemporarilyUnavailable
	}
	return HardwareFault
}
func depth(v Value) int {
	return v.Depth()
}
func encodeResults(results []ReadResult) ([]byte, error) {
	var parts [][]byte
	for _, r := range results {
		if r.Error != nil {
			parts = append(parts, ber.Uint(0x80, uint64(dataError(r.Error))))
		} else {
			b, err := EncodeValue(r.Value)
			if err != nil {
				return nil, err
			}
			parts = append(parts, b)
		}
	}
	return join(parts), nil
}
func join(parts [][]byte) []byte {
	var n int
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func (a *Association) dispatch(m envelope) []byte {
	ctx, cancel := context.WithTimeout(a.ctx, a.server.cfg.RequestTimeout)
	defer cancel()
	id, e := m.id, m.service
	success := func(data []byte) []byte {
		response := confirmed(0xa1, id, data)
		if len(response) > a.params.MaxPDUSize {
			return serviceFailure(id, 4, 3)
		}
		return response
	}
	fail := func(err error) []byte { return a.failure(id, err) }
	switch e.Tag {
	case 0x82:
		if len(e.Bytes) != 0 {
			return fail(ErrProtocol)
		}
		i := a.server.Identity
		return success(ber.Encode(0xa2, ber.Encode(0x80, []byte(i.Vendor)), ber.Encode(0x81, []byte(i.Model)), ber.Encode(0x82, []byte(i.Revision))))
	case 0x80:
		if len(e.Bytes) != 1 {
			return fail(ErrProtocol)
		}
		s := a.server.Status
		return success(ber.Encode(0xa0, ber.Uint(0x80, uint64(s.Logical)), ber.Uint(0x81, uint64(s.Physical))))
	case 0xa1:
		return a.directory(ctx, id, e)
	case 0xa6:
		n, err := single(e.Bytes, 0xa0)
		if err != nil {
			return fail(err)
		}
		nameElem, rest, err := ber.Parse(n.Bytes)
		if err != nil || len(rest) != 0 {
			return fail(ErrProtocol)
		}
		name, err := decodeName(nameElem)
		if err != nil {
			return fail(err)
		}
		t, err := a.server.Store.Type(ctx, name)
		if err != nil {
			return fail(err)
		}
		if TypeDepth(t) > a.params.NestingLevel {
			return fail(TypeUnsupported)
		}
		encoded, err := EncodeType(t)
		if err != nil {
			return fail(err)
		}
		return success(ber.Encode(0xa6, ber.Encode(0x80, []byte{0}), ber.Encode(0xa2, encoded)))
	case 0xa4:
		fields, err := ber.ParseAll(e.Bytes)
		if err != nil || len(fields) == 0 || len(fields) > 2 {
			return fail(ErrProtocol)
		}
		specWithResult := false
		if len(fields) == 2 {
			if fields[0].Tag != 0x80 || len(fields[0].Bytes) != 1 {
				return fail(ErrProtocol)
			}
			specWithResult = fields[0].Bytes[0] != 0
		}
		spec := fields[len(fields)-1]
		if spec.Tag != 0xa1 {
			return fail(ErrProtocol)
		}
		accessSpec, rest, err := ber.Parse(spec.Bytes)
		if err != nil || len(rest) != 0 {
			return fail(ErrProtocol)
		}
		accesses, err := a.resolveAccess(accessSpec)
		if err != nil {
			return fail(err)
		}
		results := make([]ReadResult, len(accesses))
		for i, access := range accesses {
			v, err := a.server.Store.Read(ctx, access)
			if err == nil && depth(v) > a.params.NestingLevel {
				err = TypeUnsupported
			}
			results[i] = ReadResult{v, err}
		}
		data, err := encodeResults(results)
		if err != nil {
			return fail(err)
		}
		var f [][]byte
		if specWithResult {
			f = append(f, ber.Encode(0xa0, ber.Encode(accessSpec.Tag, accessSpec.Bytes)))
		}
		f = append(f, ber.Encode(0xa1, data))
		return success(ber.Encode(0xa4, f...))
	case 0xa5:
		fields, err := ber.ParseAll(e.Bytes)
		if err != nil || len(fields) != 2 || fields[1].Tag != 0xa0 {
			return fail(ErrProtocol)
		}
		accesses, err := a.resolveAccess(fields[0])
		if err != nil {
			return fail(err)
		}
		encoded, err := ber.ParseAll(fields[1].Bytes)
		if err != nil || len(encoded) != len(accesses) {
			return fail(ErrProtocol)
		}
		// Decode all values before the first write: malformed data must not cause
		// a partially executed request. Valid items can still fail individually.
		values := make([]Value, len(encoded))
		for i, item := range encoded {
			values[i], err = DecodeValue(ber.Encode(item.Tag, item.Bytes))
			if err != nil {
				return fail(ErrProtocol)
			}
		}
		var results [][]byte
		for i, access := range accesses {
			var err error
			if depth(values[i]) > a.params.NestingLevel {
				err = TypeUnsupported
			} else {
				err = a.server.Store.Write(ctx, access, values[i])
			}
			if err != nil {
				results = append(results, ber.Uint(0x80, uint64(dataError(err))))
			} else {
				results = append(results, ber.Encode(0x81))
			}
		}
		return success(ber.Encode(0xa5, results...))
	case 0xab, 0xac, 0xad:
		return a.listService(ctx, id, e)
	default:
		return reject(id, 1, 1)
	}
}
func (a *Association) directory(ctx context.Context, id uint32, e ber.Element) []byte {
	fail := func(err error) []byte { return a.failure(id, err) }
	fields, err := ber.ParseAll(e.Bytes)
	if err != nil || len(fields) < 2 || len(fields) > 3 || fields[0].Tag != 0xa0 || fields[1].Tag != 0xa1 {
		return fail(ErrProtocol)
	}
	classElem, err := single(fields[0].Bytes, 0x80)
	if err != nil {
		return fail(err)
	}
	class, err := unsigned(classElem)
	if err != nil {
		return fail(err)
	}
	scopeElem, rest, err := ber.Parse(fields[1].Bytes)
	if err != nil || len(rest) != 0 || scopeElem.Tag < 0x80 || scopeElem.Tag > 0x82 {
		return fail(ErrProtocol)
	}
	scope := Scope(scopeElem.Tag - 0x80)
	domain := string(scopeElem.Bytes)
	if scope == DomainScope {
		if !validIdentifier(domain) {
			return fail(ErrProtocol)
		}
	} else if domain != "" {
		return fail(ErrProtocol)
	}
	after := ""
	if len(fields) == 3 {
		if fields[2].Tag != 0x82 || !validIdentifier(string(fields[2].Bytes)) {
			return fail(ErrProtocol)
		}
		after = string(fields[2].Bytes)
	}
	var names []string
	switch class {
	case uint32(DomainObject):
		if scope != VMDScope {
			return fail(&ServiceError{7, 1})
		}
		names, err = a.server.Store.Domains(ctx)
	case uint32(NamedVariable):
		names, err = a.server.Store.Names(ctx, scope, domain)
	case uint32(NamedVariableList):
		if scope == DomainScope {
			domains, e := a.server.Store.Domains(ctx)
			if e != nil {
				return fail(e)
			}
			found := false
			for _, n := range domains {
				found = found || n == domain
			}
			if !found {
				return fail(&ServiceError{7, 2})
			}
		}
		names = a.listNames(scope, domain)
	default:
		return fail(&ServiceError{7, 1})
	}
	if err != nil {
		return fail(err)
	}
	sort.Strings(names)
	var items [][]byte
	start := sort.SearchStrings(names, after)
	for start < len(names) && names[start] <= after {
		start++
	}
	more := false
	for i := start; i < len(names); i++ {
		if !validIdentifier(names[i]) {
			return fail(ErrProtocol)
		}
		candidate := append(items, ber.Encode(0x1a, []byte(names[i])))
		pdu := confirmed(0xa1, id, ber.Encode(0xa1, ber.Encode(0xa0, candidate...), ber.Encode(0x81, []byte{0xff})))
		if len(pdu) > a.params.MaxPDUSize {
			more = true
			break
		}
		items = candidate
	}
	if more && len(items) == 0 {
		return serviceFailure(id, 4, 3)
	}
	flag := byte(0)
	if more {
		flag = 0xff
	}
	return confirmed(0xa1, id, ber.Encode(0xa1, ber.Encode(0xa0, items...), ber.Encode(0x81, []byte{flag})))
}
