// SPDX-License-Identifier: GPL-3.0-or-later
//go:build linux

package ethernet

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"syscall"
)

const (
	solPacket             = 263
	packetAddMembership   = 1
	packetAuxdata         = 8
	packetMRAllMulti      = 2
	packetMRPromisc       = 1
	tpStatusVLANValid     = 1 << 4
	tpStatusVLANTPIDValid = 1 << 6
)

func Interfaces() ([]Interface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]Interface, 0, len(interfaces))
	for _, i := range interfaces {
		info := Interface{Name: i.Name, Description: i.Name}
		copy(info.MAC[:], i.HardwareAddr)
		if i.Flags&net.FlagUp != 0 {
			info.Flags |= FlagUp | FlagRunning
		}
		if i.Flags&net.FlagLoopback != 0 {
			info.Flags |= FlagLoopback
		}
		out = append(out, info)
	}
	return out, nil
}

type linuxBackend struct {
	fd      int
	index   int
	buffer  []byte
	control []byte
}

func networkOrder(v uint16) uint16 { return binary.NativeEndian.Uint16([]byte{byte(v >> 8), byte(v)}) }

func openBackend(cfg Config) (_ backend, _ Interface, err error) {
	iface, err := net.InterfaceByName(cfg.Interface)
	if err != nil {
		return nil, Interface{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if len(iface.HardwareAddr) != 6 || iface.Flags&net.FlagLoopback != 0 {
		return nil, Interface{}, fmt.Errorf("%w: interface is not Ethernet", ErrUnsupported)
	}
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW|syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC, int(networkOrder(3)))
	if err != nil {
		return nil, Interface{}, fmt.Errorf("%w: AF_PACKET requires CAP_NET_RAW: %v", ErrUnavailable, err)
	}
	success := false
	defer func() {
		if !success {
			syscall.Close(fd)
		}
	}()
	if err = syscall.Bind(fd, &syscall.SockaddrLinklayer{Ifindex: iface.Index, Protocol: networkOrder(3)}); err != nil {
		return nil, Interface{}, fmt.Errorf("ethernet: bind: %w", err)
	}
	// Socket-local memberships avoid changing global interface flags. GOOSE
	// receivers require multicast, including groups chosen by the application.
	membership := make([]byte, 16) // struct packet_mreq: int32, uint16, uint16, address[8]
	binary.NativeEndian.PutUint32(membership[:4], uint32(iface.Index))
	kind := uint16(packetMRAllMulti)
	if cfg.Promiscuous {
		kind = packetMRPromisc
	}
	binary.NativeEndian.PutUint16(membership[4:6], kind)
	if err = syscall.SetsockoptString(fd, solPacket, packetAddMembership, string(membership)); err != nil {
		return nil, Interface{}, fmt.Errorf("ethernet: multicast membership: %w", err)
	}
	if err = syscall.SetsockoptInt(fd, solPacket, packetAuxdata, 1); err != nil {
		return nil, Interface{}, fmt.Errorf("ethernet: VLAN auxiliary data: %w", err)
	}
	info := Interface{Name: iface.Name, Description: iface.Name, Flags: FlagUp | FlagRunning}
	copy(info.MAC[:], iface.HardwareAddr)
	success = true
	return &linuxBackend{fd: fd, index: iface.Index, buffer: make([]byte, maxCaptureSize+1), control: make([]byte, 256)}, info, nil
}

func (b *linuxBackend) read() (capturedPacket, error) {
	n, controlLen, flags, _, err := syscall.Recvmsg(b.fd, b.buffer, b.control, syscall.MSG_DONTWAIT|syscall.MSG_TRUNC)
	if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EINTR) {
		return capturedPacket{}, nil
	}
	if err != nil {
		return capturedPacket{}, err
	}
	truncated := flags&syscall.MSG_TRUNC != 0 || n > len(b.buffer)
	n = min(n, len(b.buffer))
	frame := append([]byte(nil), b.buffer[:n]...)
	if flags&syscall.MSG_CTRUNC != 0 {
		return capturedPacket{}, fmt.Errorf("ethernet: truncated VLAN metadata")
	}
	controls, err := syscall.ParseSocketControlMessage(b.control[:controlLen])
	if err != nil {
		return capturedPacket{}, err
	}
	for _, c := range controls {
		if c.Header.Level != solPacket || c.Header.Type != packetAuxdata {
			continue
		}
		frame, err = restoreVLAN(frame, c.Data)
		if err != nil {
			return capturedPacket{}, err
		}
	}
	return capturedPacket{data: frame, truncated: truncated}, nil
}

// tpacket_auxdata fields use host byte order. VLAN metadata describes a tag
// stripped by the device/kernel; prepend it even if an inner tag is present.
func restoreVLAN(frame, aux []byte) ([]byte, error) {
	if len(aux) < 20 {
		return nil, fmt.Errorf("ethernet: short VLAN auxiliary data")
	}
	status := binary.NativeEndian.Uint32(aux[:4])
	if status&tpStatusVLANValid == 0 {
		return frame, nil
	}
	if len(frame) < 14 {
		return nil, fmt.Errorf("ethernet: short Ethernet frame for VLAN metadata")
	}
	tpid := uint16(0x8100)
	if status&tpStatusVLANTPIDValid != 0 {
		tpid = binary.NativeEndian.Uint16(aux[18:20])
		if tpid != 0x8100 && tpid != 0x88a8 {
			return nil, fmt.Errorf("ethernet: unsupported auxiliary VLAN TPID %#x", tpid)
		}
	}
	tci := binary.NativeEndian.Uint16(aux[16:18])
	out := make([]byte, len(frame)+4)
	copy(out, frame[:12])
	binary.BigEndian.PutUint16(out[12:14], tpid)
	binary.BigEndian.PutUint16(out[14:16], tci)
	copy(out[16:], frame[12:])
	return out, nil
}

func (b *linuxBackend) write(frame []byte) error {
	var address [8]byte
	copy(address[:], frame[:6])
	return syscall.Sendto(b.fd, frame, syscall.MSG_DONTWAIT, &syscall.SockaddrLinklayer{Ifindex: b.index, Halen: 6, Addr: address, Protocol: networkOrder(binary.BigEndian.Uint16(frame[12:14]))})
}
func (b *linuxBackend) close() error { return syscall.Close(b.fd) }
