// SPDX-License-Identifier: GPL-3.0-or-later
// Package memory 为示例提供有界内存帧链路，不打开真实网卡。
// 它实现与网卡相同的 FrameIO 方法；WriteFrame 复制字节，避免调用方改写已发送帧。
package memory

import (
	"bytes"
	"context"
	"net"
	"sync"
)

// Link 同时满足 goose.FrameIO 和 sv.FrameIO。队列满时等待 context 或读取方，
// 不是丢包队列；本例先 Publish 再 ReadFrame，不需要后台 goroutine。
type Link struct {
	frames chan []byte
	done   chan struct{}
	once   sync.Once
}

func NewLink() *Link { return &Link{frames: make(chan []byte, 16), done: make(chan struct{})} }

func (l *Link) WriteFrame(ctx context.Context, frame []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-l.done:
		return net.ErrClosed
	default:
	}
	select {
	case l.frames <- bytes.Clone(frame):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-l.done:
		return net.ErrClosed
	}
}

func (l *Link) ReadFrame(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-l.done:
		return nil, net.ErrClosed
	default:
	}
	select {
	case frame := <-l.frames:
		return frame, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *Link) Close() error { l.once.Do(func() { close(l.done) }); return nil }
