# Platform support and known limits

English | [简体中文](platforms.zh-CN.md)

The protocol core is pure Go. MMS uses TCP; GOOSE/SV use Ethernet, so TCP loopback cannot replace Layer 2 testing.

| Platform | Access method | Requirements |
|---|---|---|
| Windows amd64 | Runtime Npcap DLL calls without cgo | Accessible Npcap driver/DLL and an Ethernet interface |
| Linux | Nonblocking AF_PACKET/SOCK_RAW | `CAP_NET_RAW` or equivalent permissions and an explicit interface name |
| Other unsupported platforms | `ErrUnsupported` | Supply a custom `FrameIO` to use the protocol state machines |

`ethernet.Open` requires an explicit interface. Windows accepts an Npcap name, full description, or list index; Linux accepts an interface name. Non-Ethernet link types such as loopback are rejected. Use `-list` on a GOOSE/SV command-line tool to inspect interfaces.

## Windows multicast delivery

Non-promiscuous capture does not automatically register the target multicast group in the interface's receive table. Some interfaces do not deliver IEC 61850 multicast in that mode; this occurred in earlier cross-VM runs. Explicitly use `-promiscuous` on a dedicated interface when required, or set `ethernet.Config{Promiscuous: true}`. Protocol and VLAN filtering still applies.

## VLAN byte fidelity

The codecs retain PCP, DEI, and the full VID. Linux access uses `PACKET_AUXDATA` to restore tags stripped by the kernel/device, but it can only restore metadata actually supplied by the operating system.

- On a previously exercised 4.19 kernel receive path, DEI 1 was cleared. An independent raw-socket probe observed the same result; the missing original bit cannot be inferred afterward.
- One Windows-to-VM path changed transmitted PCP 4 to received PCP 0, while the reverse direction retained PCP 4. Protocol decoding cannot restore metadata lost along the path.
- Linux veth checks with DEI 0 strictly retained high PCP and VID values. DEI 1 is a separate capability probe with strict checks by default. Only when `ETHERNET_LIVE_ALLOW_DEI_LOSS=1` is explicitly set and all other bytes match is the known DEI-only difference recorded as SKIP.

Successful protocol interoperability does not establish fidelity for every VLAN field on a particular hardware path. Compare captures and OS metadata at both ends when deploying. Historical raw evidence remains in ignored `_local/reports/`.

## Scheduling, synchronization, and shutdown

GOOSE retransmits according to configuration and uses TAL. SV sends on software time slots, skips expired slots when late, and advances sample counters. Go scheduling, system load, drivers, and NICs affect latency. The library makes no protection-grade hard real-time guarantee.

SV examples report unsynchronized data by default; they do not establish PTP or a hardware sampling clock. SampleSync, SampleRate, and SampleMode are metadata fields and do not create a synchronization source or acquisition hardware.

Native reads use nonblocking polling with context cancellation and coordinated close. Once injection enters the operating system, it cannot be safely interrupted forcibly; write cancellation is checked before the call. Timing, remote IED behavior, physical switches, and hardware offload require checks in the corresponding environment.

[Ethernet adapter](../ethernet/README.md) · [Command-line tools](../cmd/README.md)
