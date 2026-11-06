// SPDX-License-Identifier: GPL-3.0-or-later
// Package cli provides shared argument and capture support for the Ethernet tools.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/mababaNiubi/go-iec61850/ethernet"
	"github.com/mababaNiubi/go-iec61850/internal/pcap"
)

type FrameIO interface {
	ReadFrame(context.Context) ([]byte, error)
	WriteFrame(context.Context, []byte) error
	Close() error
}

func List(out io.Writer) error {
	interfaces, err := ethernet.Interfaces()
	if err != nil {
		return err
	}
	for i, item := range interfaces {
		fmt.Fprintf(out, "%d  %s  %s\n   %s\n", i, item.Description, net.HardwareAddr(item.MAC[:]), item.Name)
	}
	return nil
}

func MAC(value string) ([6]byte, error) {
	var out [6]byte
	mac, err := net.ParseMAC(value)
	if err != nil || len(mac) != len(out) {
		return out, fmt.Errorf("invalid Ethernet MAC %q", value)
	}
	copy(out[:], mac)
	return out, nil
}

type recordedLink struct {
	FrameIO
	capture *pcap.Writer
}

func (link *recordedLink) ReadFrame(ctx context.Context) ([]byte, error) {
	frame, err := link.FrameIO.ReadFrame(ctx)
	if len(frame) > 0 {
		if recordErr := link.capture.WriteFrame(time.Now(), frame); recordErr != nil {
			return nil, recordErr
		}
	}
	return frame, err
}

func (link *recordedLink) WriteFrame(ctx context.Context, frame []byte) error {
	if err := link.FrameIO.WriteFrame(ctx, frame); err != nil {
		return err
	}
	return link.capture.WriteFrame(time.Now(), frame)
}

// Record leaves ownership of the link with the caller. The returned close
// function closes only the optional capture file.
func Record(link FrameIO, path string) (FrameIO, func() error, error) {
	if path == "" {
		return link, func() error { return nil }, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, nil, err
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	writer, err := pcap.NewWriter(file)
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return &recordedLink{link, writer}, file.Close, nil
}

// WriteFrameFile also creates the parent directory requested by the caller.
func WriteFrameFile(path string, frame []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, frame, 0644)
}

// Complete distinguishes an intentional duration from an unmet requested count.
func Complete(err error, received, requested int, unit string) error {
	if requested > 0 && received < requested && errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("timed out after receiving %d of %d requested %s", received, requested, unit)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}
