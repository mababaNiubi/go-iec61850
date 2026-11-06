// SPDX-License-Identifier: GPL-3.0-or-later
// Package pcap writes classic PCAP with Ethernet datalink and microsecond time.
package pcap

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"
)

type Writer struct {
	mu  sync.Mutex
	out io.Writer
}

func NewWriter(out io.Writer) (*Writer, error) {
	if out == nil {
		return nil, fmt.Errorf("pcap: nil output")
	}
	h := make([]byte, 24)
	binary.LittleEndian.PutUint32(h, 0xa1b2c3d4)
	binary.LittleEndian.PutUint16(h[4:], 2)
	binary.LittleEndian.PutUint16(h[6:], 4)
	binary.LittleEndian.PutUint32(h[16:], 65535)
	binary.LittleEndian.PutUint32(h[20:], 1)
	if err := writeAll(out, h); err != nil {
		return nil, err
	}
	return &Writer{out: out}, nil
}
func writeAll(out io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := out.Write(b)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(b) {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
func (w *Writer) WriteFrame(t time.Time, frame []byte) error {
	if len(frame) > 65535 || t.Unix() < 0 || t.Unix() > 0xffffffff {
		return fmt.Errorf("pcap: invalid frame/time")
	}
	h := make([]byte, 16)
	binary.LittleEndian.PutUint32(h, uint32(t.Unix()))
	binary.LittleEndian.PutUint32(h[4:], uint32(t.Nanosecond()/1000))
	binary.LittleEndian.PutUint32(h[8:], uint32(len(frame)))
	binary.LittleEndian.PutUint32(h[12:], uint32(len(frame)))
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := writeAll(w.out, h); err != nil {
		return err
	}
	return writeAll(w.out, frame)
}
