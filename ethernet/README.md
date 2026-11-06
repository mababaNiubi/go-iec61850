# Ethernet access

English | [简体中文](README.zh-CN.md)

Optional raw Ethernet adapters for GOOSE and SV. The protocol core uses `FrameIO`, so applications may also supply an in-memory link or another transport.

Import `github.com/mababaNiubi/go-iec61850/ethernet` and `github.com/mababaNiubi/go-iec61850/sv`. Inside a function returning an error:

```go
conn, err := ethernet.Open(ethernet.Config{
    Interface: "eth0", // Replace with an actual interface returned by Interfaces().
    EtherType: sv.EtherType,
})
if err != nil { return err }
defer conn.Close()
```

Windows amd64 loads Npcap DLLs from system Npcap locations without cgo. Linux uses nonblocking AF_PACKET, socket-local multicast membership, and PACKET_AUXDATA. Opening a raw socket requires `CAP_NET_RAW` or equivalent permissions. Other unsupported platforms return `ErrUnsupported`.

Select an interface explicitly. Windows accepts an Npcap name, full description, or list index; Linux uses the interface name. `Conn.Interface()` and `Conn.MAC()` return the selected interface and source address. Non-Ethernet link types, including loopback, are rejected.

Reads use nonblocking polling with context cancellation and coordinated `Close`. Returned frames own their bytes, include Ethernet headers and optional padding, and exclude the FCS. EtherType filtering recognizes untagged, single-tagged, and double-tagged traffic; each protocol codec still validates its own profile.

Some Windows interfaces need promiscuous capture to deliver multicast groups absent from the address table. Subscriber tools expose `-promiscuous`. VLAN restoration on Linux depends on the supplied kernel metadata. Older-kernel DEI loss and virtual-link PCP changes have been observed; see [platform support](../docs/platforms.md).

Ordinary unit tests use mock drivers and do not choose a NIC. Live-interface checks require explicit environment variables and a dedicated test interface; see [development](../docs/development.md).
