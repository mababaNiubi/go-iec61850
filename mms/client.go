// SPDX-License-Identifier: GPL-3.0-or-later
package mms

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/mababaNiubi/go-iec61850/internal/ber"
	"github.com/mababaNiubi/go-iec61850/internal/iso"
)

type callResult struct {
	service ber.Element
	err     error
}
type ReadResult struct {
	Value Value
	Error error
}

// InformationReport is a raw MMS report, before IEC 61850 report mapping.
type InformationReport struct {
	List      *ObjectName
	Variables []VariableAccess
	Results   []ReadResult
}

// Client multiplexes concurrent confirmed requests on one MMS association.
type Client struct {
	conn         *iso.Conn
	cfg          Config
	params       Parameters
	mu           sync.Mutex
	nextID       uint32
	pending      map[uint32]chan callResult
	closing      bool
	err          error
	done         chan struct{}
	window       chan struct{}
	sendToken    chan struct{}
	concluded    chan struct{}
	concludeOnce sync.Once
	reports      chan InformationReport
}

// Dial 建立到 address（host:port）的 ISO/MMS 关联。ctx 控制本次建连与协商；
// 后续读写使用各自的 context，应用仍需 Conclude 或 Close 释放 Client。
func Dial(ctx context.Context, address string, cfg Config) (*Client, error) {
	cfg, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	local := localParameters(cfg)
	conn, response, err := iso.Dial(ctx, address, cfg.ISO, encodeInitiate(0xa8, local))
	if err != nil {
		return nil, err
	}
	peer, err := decodeInitiate(response, 0xa9)
	if err != nil {
		conn.Close()
		return nil, err
	}
	p := negotiate(local, peer)
	p.Services = peer.Services
	c := &Client{conn: conn, cfg: cfg, params: p, pending: make(map[uint32]chan callResult), done: make(chan struct{}), window: make(chan struct{}, p.MaxOutstandingCalling), sendToken: make(chan struct{}, 1), concluded: make(chan struct{}), reports: make(chan InformationReport, 64)}
	go c.receive()
	return c, nil
}
func (c *Client) Parameters() Parameters            { return c.params }
func (c *Client) Reports() <-chan InformationReport { return c.reports }
func (c *Client) Done() <-chan struct{}             { return c.done }
func (c *Client) Err() error                        { c.mu.Lock(); defer c.mu.Unlock(); return c.err }
func (c *Client) shutdown(err error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	if err == nil {
		err = ErrClosed
	}
	c.err = err
	close(c.done)
	for id, ch := range c.pending {
		ch <- callResult{err: err}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	c.conn.Close()
}
func (c *Client) Close() error { c.shutdown(ErrClosed); return nil }

func sendPDU(ctx context.Context, conn *iso.Conn, token chan struct{}, data []byte) error {
	return writeContext(ctx, conn, token, func() error { return conn.WritePDU(data) })
}

func writeContext(ctx context.Context, conn *iso.Conn, token chan struct{}, write func() error) error {
	select {
	case token <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-token }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if d, ok := ctx.Deadline(); ok {
		if err := conn.SetWriteDeadline(d); err != nil {
			return err
		}
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { conn.SetWriteDeadline(time.Now()); close(interrupted) })
	err := write()
	if !stop() {
		<-interrupted
	}
	conn.SetWriteDeadline(time.Time{})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
func (c *Client) receive() {
	defer close(c.reports)
	for {
		data, err := c.conn.ReadPDU()
		if err != nil {
			c.shutdown(err)
			return
		}
		if len(data) > c.params.MaxPDUSize {
			c.shutdown(ErrProtocol)
			return
		}
		msg, err := decodeEnvelope(data)
		if err != nil {
			c.shutdown(err)
			return
		}
		switch msg.tag {
		case 0x8c:
			c.mu.Lock()
			requested := c.closing
			c.mu.Unlock()
			if !requested {
				c.shutdown(ErrProtocol)
				return
			}
			select {
			case <-c.concluded:
				c.shutdown(ErrProtocol)
				return
			default:
			}
			c.concludeOnce.Do(func() { close(c.concluded) })
		case 0x8b:
			ctx, cancel := context.WithTimeout(context.Background(), c.cfg.RequestTimeout)
			err = sendPDU(ctx, c.conn, c.sendToken, []byte{0x8c, 0})
			cancel()
			if err != nil {
				c.shutdown(err)
				return
			}
			c.shutdown(ErrClosed)
			return
		case 0xa3:
			r, err := decodeReport(msg.service)
			if err == nil {
				err = checkResultDepth(r.Results, c.params.NestingLevel)
			}
			if err != nil {
				c.shutdown(err)
				return
			}
			select {
			case c.reports <- r:
			default:
				c.shutdown(ErrReportOverflow)
				return
			}
		case 0xa1, 0xa2, 0xa4:
			c.mu.Lock()
			ch := c.pending[msg.id]
			if ch != nil {
				delete(c.pending, msg.id)
				ch <- callResult{msg.service, msg.err}
			}
			c.mu.Unlock()
		default:
			c.shutdown(ErrProtocol)
			return
		}
	}
}
func (c *Client) call(ctx context.Context, request []byte, responseTag byte) (ber.Element, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return ber.Element{}, err
	}
	select {
	case c.window <- struct{}{}:
	case <-ctx.Done():
		return ber.Element{}, ctx.Err()
	case <-c.done:
		return ber.Element{}, c.Err()
	}
	defer func() { <-c.window }()
	ch := make(chan callResult, 1)
	c.mu.Lock()
	if c.err != nil || c.closing {
		err := c.err
		if err == nil {
			err = ErrClosed
		}
		c.mu.Unlock()
		return ber.Element{}, err
	}
	for {
		c.nextID++
		if c.nextID != 0 && c.pending[c.nextID] == nil {
			break
		}
	}
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	data := confirmed(0xa0, id, request)
	if len(data) > c.params.MaxPDUSize {
		return ber.Element{}, fmt.Errorf("mms: request exceeds negotiated PDU size")
	}
	if err := sendPDU(ctx, c.conn, c.sendToken, data); err != nil {
		c.shutdown(err)
		return ber.Element{}, err
	}
	select {
	case result := <-ch:
		if result.err != nil {
			return ber.Element{}, result.err
		}
		if result.service.Tag != responseTag {
			c.shutdown(ErrProtocol)
			return ber.Element{}, ErrProtocol
		}
		return result.service, nil
	case <-ctx.Done():
		// A sent call may still occupy the peer's negotiated window. Closing
		// avoids reusing its slot while a late response remains outstanding.
		c.shutdown(ctx.Err())
		return ber.Element{}, ctx.Err()
	case <-c.done:
		return ber.Element{}, c.Err()
	}
}

// Conclude gracefully concludes MMS, then releases the ISO association.
// It requires that no confirmed calls are in progress.
func (c *Client) Conclude(ctx context.Context) error {
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	if c.closing || len(c.pending) > 0 {
		c.mu.Unlock()
		return fmt.Errorf("mms: association busy or closing")
	}
	c.closing = true
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	defer cancel()
	defer c.Close()
	if err := sendPDU(ctx, c.conn, c.sendToken, []byte{0x8b, 0}); err != nil {
		return err
	}
	select {
	case <-c.concluded:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.Err()
	}
	if err := writeContext(ctx, c.conn, c.sendToken, c.conn.Release); err != nil {
		return err
	}
	select {
	case <-c.done:
		if errors.Is(c.Err(), io.EOF) {
			return nil
		}
		return c.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *Client) Identify(ctx context.Context) (Identity, error) {
	e, err := c.call(ctx, ber.Encode(0x82), 0xa2)
	if err != nil {
		return Identity{}, err
	}
	f, err := ber.ParseAll(e.Bytes)
	if err != nil || len(f) < 3 || f[0].Tag != 0x80 || f[1].Tag != 0x81 || f[2].Tag != 0x82 {
		return Identity{}, ErrProtocol
	}
	return Identity{string(f[0].Bytes), string(f[1].Bytes), string(f[2].Bytes)}, nil
}
func (c *Client) Status(ctx context.Context) (Status, error) {
	e, err := c.call(ctx, ber.Encode(0x80, []byte{0}), 0xa0)
	if err != nil {
		return Status{}, err
	}
	f, err := ber.ParseAll(e.Bytes)
	if err != nil || len(f) < 2 || f[0].Tag != 0x80 || f[1].Tag != 0x81 {
		return Status{}, ErrProtocol
	}
	l, err := unsigned(f[0])
	if err != nil {
		return Status{}, err
	}
	p, err := unsigned(f[1])
	return Status{int(l), int(p)}, err
}

type NamePage struct {
	Names       []string
	MoreFollows bool
}

func (c *Client) GetNameList(ctx context.Context, class ObjectClass, scope Scope, domain, after string) (NamePage, error) {
	if scope > AssociationScope || (scope == DomainScope && !validIdentifier(domain)) || (scope != DomainScope && domain != "") || (after != "" && !validIdentifier(after)) {
		return NamePage{}, ErrProtocol
	}
	var domainBytes []byte
	if scope == DomainScope {
		domainBytes = []byte(domain)
	}
	f := [][]byte{ber.Encode(0xa0, ber.Uint(0x80, uint64(class))), ber.Encode(0xa1, ber.Encode(0x80+byte(scope), domainBytes))}
	if after != "" {
		f = append(f, ber.Encode(0x82, []byte(after)))
	}
	e, err := c.call(ctx, ber.Encode(0xa1, f...), 0xa1)
	if err != nil {
		return NamePage{}, err
	}
	fields, err := ber.ParseAll(e.Bytes)
	if err != nil || len(fields) < 1 || len(fields) > 2 || fields[0].Tag != 0xa0 {
		return NamePage{}, ErrProtocol
	}
	items, err := ber.ParseAll(fields[0].Bytes)
	if err != nil {
		return NamePage{}, err
	}
	page := NamePage{MoreFollows: true}
	for _, n := range items {
		if n.Tag != 0x1a || !validIdentifier(string(n.Bytes)) {
			return NamePage{}, ErrProtocol
		}
		page.Names = append(page.Names, string(n.Bytes))
	}
	if len(fields) == 2 {
		if fields[1].Tag != 0x81 || len(fields[1].Bytes) != 1 {
			return NamePage{}, ErrProtocol
		}
		page.MoreFollows = fields[1].Bytes[0] != 0
	}
	return page, nil
}

// Names 自动获取并拼接目录分页。查 Domain 用 DomainObject/VMDScope/空domain；
// 查域内变量用 NamedVariable/DomainScope/实际domain。
func (c *Client) Names(ctx context.Context, class ObjectClass, scope Scope, domain string) ([]string, error) {
	var out []string
	after := ""
	for {
		page, err := c.GetNameList(ctx, class, scope, domain, after)
		if err != nil {
			return nil, err
		}
		for _, n := range page.Names {
			if n <= after {
				return nil, ErrProtocol
			}
			after = n
			out = append(out, n)
		}
		if !page.MoreFollows {
			return out, nil
		}
		if len(page.Names) == 0 {
			return nil, ErrProtocol
		}
	}
}

// GetVariableAccessAttributes 查询设备声明的类型、宽度、长度和结构成员，
// 便于读取前选择访问器，或用 ValidateValue 校验准备写入的值。
func (c *Client) GetVariableAccessAttributes(ctx context.Context, n ObjectName) (TypeSpec, error) {
	if err := n.validate(); err != nil {
		return TypeSpec{}, err
	}
	e, err := c.call(ctx, ber.Encode(0xa6, ber.Encode(0xa0, encodeName(n))), 0xa6)
	if err != nil {
		return TypeSpec{}, err
	}
	f, err := ber.ParseAll(e.Bytes)
	if err != nil || len(f) < 2 || f[0].Tag != 0x80 || len(f[0].Bytes) != 1 {
		return TypeSpec{}, ErrProtocol
	}
	for _, x := range f[1:] {
		if x.Tag == 0xa2 {
			t, err := DecodeType(x.Bytes)
			if err == nil && TypeDepth(t) > c.params.NestingLevel {
				err = ErrProtocol
			}
			if err != nil {
				c.shutdown(err)
			}
			return t, err
		}
	}
	return TypeSpec{}, ErrProtocol
}
func decodeResults(data []byte) ([]ReadResult, error) {
	items, err := ber.ParseAll(data)
	if err != nil {
		return nil, err
	}
	out := make([]ReadResult, 0, len(items))
	for _, item := range items {
		if item.Tag == 0x80 {
			v, err := unsigned(item)
			if err != nil || v > 11 {
				return nil, ErrProtocol
			}
			out = append(out, ReadResult{Error: DataAccessError(v)})
		} else {
			v, err := DecodeValue(ber.Encode(item.Tag, item.Bytes))
			if err != nil {
				return nil, err
			}
			out = append(out, ReadResult{Value: v})
		}
	}
	return out, nil
}

func checkResultDepth(results []ReadResult, limit int) error {
	for _, r := range results {
		if r.Error == nil && depth(r.Value) > limit {
			return ErrProtocol
		}
	}
	return nil
}
func (c *Client) read(ctx context.Context, spec []byte, expected int) ([]ReadResult, error) {
	e, err := c.call(ctx, ber.Encode(0xa4, ber.Encode(0xa1, spec)), 0xa4)
	if err != nil {
		return nil, err
	}
	f, err := ber.ParseAll(e.Bytes)
	if err != nil || len(f) == 0 || f[len(f)-1].Tag != 0xa1 {
		return nil, ErrProtocol
	}
	r, err := decodeResults(f[len(f)-1].Bytes)
	if err == nil {
		err = checkResultDepth(r, c.params.NestingLevel)
	}
	if err != nil {
		c.shutdown(err)
	}
	if err == nil && expected >= 0 && len(r) != expected {
		return nil, ErrProtocol
	}
	return r, err
}

// ReadMultiple 按 accesses 顺序批量读取。返回的 error 针对整个请求；
// err 为 nil 时仍须逐项检查 ReadResult.Error，成功项再读取 Value。
func (c *Client) ReadMultiple(ctx context.Context, accesses ...VariableAccess) ([]ReadResult, error) {
	spec, err := encodeAccesses(accesses)
	if err != nil {
		return nil, err
	}
	return c.read(ctx, spec, len(accesses))
}

// ReadAccess 读取一个带选择的变量，配合 Element（0起始数组下标）或 Member 使用。
func (c *Client) ReadAccess(ctx context.Context, access VariableAccess) (Value, error) {
	r, err := c.ReadMultiple(ctx, access)
	if err != nil {
		return Value{}, err
	}
	return r[0].Value, r[0].Error
}

// Read 读取 n 标识的完整变量。返回值需通过 Int、Bool、Float 等访问器转换；
// n 的 Domain/Item 是对端对象标识，不是打印形式的 "domain/item" 单字符串。
func (c *Client) Read(ctx context.Context, n ObjectName) (Value, error) {
	return c.ReadAccess(ctx, Access(n))
}
func (c *Client) write(ctx context.Context, spec []byte, values []Value) ([]error, error) {
	var encoded [][]byte
	for _, v := range values {
		if depth(v) > c.params.NestingLevel {
			return nil, fmt.Errorf("mms: value exceeds negotiated nesting level")
		}
		b, err := EncodeValue(v)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, b)
	}
	e, err := c.call(ctx, ber.Encode(0xa5, spec, ber.Encode(0xa0, encoded...)), 0xa5)
	if err != nil {
		return nil, err
	}
	f, err := ber.ParseAll(e.Bytes)
	if err != nil || len(f) != len(values) {
		return nil, ErrProtocol
	}
	out := make([]error, len(f))
	for i, x := range f {
		switch x.Tag {
		case 0x81:
			if len(x.Bytes) != 0 {
				return nil, ErrProtocol
			}
		case 0x80:
			v, err := unsigned(x)
			if err != nil || v > 11 {
				return nil, ErrProtocol
			}
			out[i] = DataAccessError(v)
		default:
			return nil, ErrProtocol
		}
	}
	return out, nil
}

// WriteMultiple 的 accesses 与 values 必须等长且顺序对应。外层 error 为请求失败，
// 返回切片的每个 error 为该项的结果；部分成功不会自动回滚。
func (c *Client) WriteMultiple(ctx context.Context, accesses []VariableAccess, values []Value) ([]error, error) {
	if len(accesses) != len(values) {
		return nil, fmt.Errorf("mms: values/accesses count mismatch")
	}
	spec, err := encodeAccesses(accesses)
	if err != nil {
		return nil, err
	}
	return c.write(ctx, spec, values)
}

// WriteAccess 写入指定数组元素或结构成员；访问选择与 ReadAccess 相同。
func (c *Client) WriteAccess(ctx context.Context, a VariableAccess, v Value) error {
	r, err := c.WriteMultiple(ctx, []VariableAccess{a}, []Value{v})
	if err != nil {
		return err
	}
	return r[0]
}

// Write 写入一个变量。用 Integer、Boolean 等构造带类型的值；
// 是否可写以及目标类型是否匹配由对端校验，调用方必须检查返回错误。
func (c *Client) Write(ctx context.Context, n ObjectName, v Value) error {
	return c.WriteAccess(ctx, Access(n), v)
}
