// SPDX-License-Identifier: GPL-3.0-or-later
//go:build windows && amd64

package ethernet

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// Windows uses 32-bit timeval longs even under the 64-bit ABI.
type pcapHeader struct {
	Seconds, Microseconds int32
	Captured, Original    uint32
}
type pcapInterface struct {
	Next              *pcapInterface
	Name, Description *byte
	Addresses         unsafe.Pointer
	Flags             uint32
}
type bpfProgram struct {
	Length       uint32
	Instructions unsafe.Pointer
}

type pcapAPI struct {
	dll                                                                                                                *syscall.DLL
	findAll, freeAll, openLive, close, nextEx, sendPacket, datalink, getErr, setNonblock, compile, setFilter, freeCode *syscall.Proc
	minToCopy                                                                                                          *syscall.Proc
}

var nativePCAP struct {
	once sync.Once
	api  *pcapAPI
	err  error
}

func loadPCAP() (*pcapAPI, error) {
	nativePCAP.once.Do(func() { nativePCAP.api, nativePCAP.err = loadPCAPOnce() })
	return nativePCAP.api, nativePCAP.err
}

func loadPCAPOnce() (*pcapAPI, error) {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	var directory [32768]uint16
	count, _, err := kernel.NewProc("GetSystemDirectoryW").Call(uintptr(unsafe.Pointer(&directory[0])), uintptr(len(directory)))
	if count == 0 || count >= uintptr(len(directory)) {
		return nil, fmt.Errorf("%w: GetSystemDirectoryW: %v", ErrUnavailable, err)
	}
	path := filepath.Join(syscall.UTF16ToString(directory[:count]), "Npcap", "wpcap.dll")
	filename, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// Restrict both the requested module and its Packet.dll dependencies to
	// the explicit Npcap directory and Windows System32.
	handle, _, loadErr := kernel.NewProc("LoadLibraryExW").Call(uintptr(unsafe.Pointer(filename)), 0, 0x100|0x800)
	runtime.KeepAlive(filename)
	if handle == 0 {
		return nil, fmt.Errorf("%w: load installed Npcap %q: %v", ErrUnavailable, path, loadErr)
	}
	dll := &syscall.DLL{Name: path, Handle: syscall.Handle(handle)}
	api := &pcapAPI{dll: dll}
	procedures := []struct {
		name   string
		target **syscall.Proc
	}{
		{"pcap_findalldevs", &api.findAll}, {"pcap_freealldevs", &api.freeAll}, {"pcap_open_live", &api.openLive}, {"pcap_close", &api.close}, {"pcap_next_ex", &api.nextEx}, {"pcap_sendpacket", &api.sendPacket}, {"pcap_datalink", &api.datalink}, {"pcap_geterr", &api.getErr}, {"pcap_setnonblock", &api.setNonblock}, {"pcap_compile", &api.compile}, {"pcap_setfilter", &api.setFilter}, {"pcap_freecode", &api.freeCode},
	}
	for _, p := range procedures {
		*p.target, err = dll.FindProc(p.name)
		if err != nil {
			dll.Release()
			return nil, fmt.Errorf("%w: %s: %v", ErrUnavailable, p.name, err)
		}
	}
	api.minToCopy, _ = dll.FindProc("pcap_setmintocopy")
	return api, nil
}

func cString(p *byte) string {
	if p == nil {
		return ""
	}
	var out []byte
	for i := 0; i < 4096; i++ {
		b := *(*byte)(unsafe.Add(unsafe.Pointer(p), i))
		if b == 0 {
			return string(out)
		}
		out = append(out, b)
	}
	return string(out)
}

func bufferString(buffer []byte) string {
	for i, b := range buffer {
		if b == 0 {
			return string(buffer[:i])
		}
	}
	return string(buffer)
}

func adapterMACs() map[string][6]byte {
	result := make(map[string][6]byte)
	length := uint32(15 << 10)
	for attempts := 0; attempts < 3; attempts++ {
		if length == 0 || length > 16<<20 {
			return result
		}
		buffer := make([]byte, length)
		head := (*syscall.IpAdapterInfo)(unsafe.Pointer(&buffer[0]))
		err := syscall.GetAdaptersInfo(head, &length)
		if err == syscall.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return result
		}
		for count, p := 0, head; p != nil && count < 4096; count, p = count+1, p.Next {
			if p.AddressLength == 6 {
				var mac [6]byte
				copy(mac[:], p.Address[:6])
				result[strings.ToUpper(bufferString(p.AdapterName[:]))] = mac
			}
		}
		runtime.KeepAlive(buffer)
		return result
	}
	return result
}

func Interfaces() ([]Interface, error) {
	api, err := loadPCAP()
	if err != nil {
		return nil, err
	}
	var devices *pcapInterface
	var errors [256]byte
	status, _, _ := api.findAll.Call(uintptr(unsafe.Pointer(&devices)), uintptr(unsafe.Pointer(&errors[0])))
	if int32(status) != 0 {
		return nil, fmt.Errorf("%w: pcap_findalldevs: %s", ErrUnavailable, bufferString(errors[:]))
	}
	defer api.freeAll.Call(uintptr(unsafe.Pointer(devices)))
	macs := adapterMACs()
	var out []Interface
	seen := make(map[*pcapInterface]bool)
	for p := devices; p != nil; p = p.Next {
		if len(out) >= 4096 || seen[p] {
			return nil, fmt.Errorf("ethernet: invalid native interface list")
		}
		seen[p] = true
		info := Interface{Name: cString(p.Name), Description: cString(p.Description), Flags: p.Flags}
		guid := strings.ToUpper(strings.TrimPrefix(info.Name, `\Device\NPF_`))
		info.MAC = macs[guid]
		out = append(out, info)
	}
	return out, nil
}

type windowsBackend struct {
	api    *pcapAPI
	handle uintptr
}

func openBackend(cfg Config) (_ backend, _ Interface, err error) {
	api, err := loadPCAP()
	if err != nil {
		return nil, Interface{}, err
	}
	interfaces, err := Interfaces()
	if err != nil {
		return nil, Interface{}, err
	}
	var selected Interface
	found := false
	for i, info := range interfaces {
		if cfg.Interface == info.Name || cfg.Interface == info.Description || cfg.Interface == strconv.Itoa(i) {
			if found {
				return nil, Interface{}, fmt.Errorf("ethernet: ambiguous interface name")
			}
			selected = info
			found = true
		}
	}
	if !found {
		return nil, Interface{}, fmt.Errorf("%w: interface %q not found; use Interfaces()", ErrUnavailable, cfg.Interface)
	}
	name, err := syscall.BytePtrFromString(selected.Name)
	if err != nil {
		return nil, Interface{}, err
	}
	var errors [256]byte
	promiscuous := uintptr(0)
	if cfg.Promiscuous {
		promiscuous = 1
	}
	handle, _, _ := api.openLive.Call(uintptr(unsafe.Pointer(name)), maxCaptureSize, promiscuous, 1, uintptr(unsafe.Pointer(&errors[0])))
	runtime.KeepAlive(name)
	if handle == 0 {
		return nil, Interface{}, fmt.Errorf("%w: pcap_open_live: %s", ErrUnavailable, bufferString(errors[:]))
	}
	driver := &windowsBackend{api: api, handle: handle}
	success := false
	defer func() {
		if !success {
			driver.close()
		}
	}()
	linktype, _, _ := api.datalink.Call(handle)
	if int32(linktype) != 1 {
		return nil, Interface{}, fmt.Errorf("%w: Npcap DLT %d is not Ethernet (DLT_EN10MB=1)", ErrUnsupported, int32(linktype))
	}
	status, _, _ := api.setNonblock.Call(handle, 1, uintptr(unsafe.Pointer(&errors[0])))
	if int32(status) != 0 {
		return nil, Interface{}, fmt.Errorf("ethernet: pcap_setnonblock: %s", bufferString(errors[:]))
	}
	if api.minToCopy != nil {
		status, _, _ = api.minToCopy.Call(handle, 0)
		if int32(status) != 0 {
			return nil, Interface{}, driver.lastError("pcap_setmintocopy")
		}
	}
	if cfg.EtherType != 0 {
		filter, err := syscall.BytePtrFromString(captureFilter(cfg.EtherType))
		if err != nil {
			return nil, Interface{}, err
		}
		var program bpfProgram
		status, _, _ = api.compile.Call(handle, uintptr(unsafe.Pointer(&program)), uintptr(unsafe.Pointer(filter)), 1, 0xffffffff)
		runtime.KeepAlive(filter)
		if int32(status) != 0 {
			return nil, Interface{}, driver.lastError("pcap_compile")
		}
		status, _, _ = api.setFilter.Call(handle, uintptr(unsafe.Pointer(&program)))
		api.freeCode.Call(uintptr(unsafe.Pointer(&program)))
		if int32(status) != 0 {
			return nil, Interface{}, driver.lastError("pcap_setfilter")
		}
	}
	success = true
	return driver, selected, nil
}

func captureFilter(kind uint16) string {
	return fmt.Sprintf("ether[12:2] = 0x%04x or ((ether[12:2] = 0x8100 or ether[12:2] = 0x88a8) and ether[16:2] = 0x%04x) or ((ether[12:2] = 0x8100 or ether[12:2] = 0x88a8) and (ether[16:2] = 0x8100 or ether[16:2] = 0x88a8) and ether[20:2] = 0x%04x)", kind, kind, kind)
}

func (b *windowsBackend) lastError(operation string) error {
	// pcap_geterr returns a foreign pointer as uintptr. Copy it entirely via
	// native APIs instead of converting uintptr into a Go pointer, which would
	// defeat Go's pointer-checking and vet rules.
	p, _, _ := b.api.getErr.Call(b.handle)
	if p == 0 {
		return fmt.Errorf("ethernet: %s failed", operation)
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	n, _, _ := kernel.NewProc("lstrlenA").Call(p)
	if n == 0 || n > 4096 {
		return fmt.Errorf("ethernet: %s failed", operation)
	}
	buffer := make([]byte, int(n))
	kernel.NewProc("RtlMoveMemory").Call(uintptr(unsafe.Pointer(&buffer[0])), p, n)
	runtime.KeepAlive(buffer)
	return fmt.Errorf("ethernet: %s: %s", operation, string(buffer))
}

func (b *windowsBackend) read() (capturedPacket, error) {
	var header *pcapHeader
	var bytes *byte
	status, _, _ := b.api.nextEx.Call(b.handle, uintptr(unsafe.Pointer(&header)), uintptr(unsafe.Pointer(&bytes)))
	switch int32(status) {
	case 0:
		return capturedPacket{}, nil
	case 1:
		if header == nil || bytes == nil || header.Captured > maxCaptureSize || header.Original < header.Captured {
			return capturedPacket{}, fmt.Errorf("ethernet: invalid native packet header")
		}
		return capturedPacket{data: append([]byte(nil), unsafe.Slice(bytes, int(header.Captured))...), truncated: header.Captured != header.Original}, nil
	default:
		return capturedPacket{}, b.lastError("pcap_next_ex")
	}
}
func (b *windowsBackend) write(frame []byte) error {
	status, _, _ := b.api.sendPacket.Call(b.handle, uintptr(unsafe.Pointer(&frame[0])), uintptr(len(frame)))
	runtime.KeepAlive(frame)
	if int32(status) != 0 {
		return b.lastError("pcap_sendpacket")
	}
	return nil
}
func (b *windowsBackend) close() error { b.api.close.Call(b.handle); return nil }
