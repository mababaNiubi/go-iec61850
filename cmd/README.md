# Command-line tools

English | [简体中文](README.zh-CN.md)

These are runnable network tools. To call the library from application code, start with the [API guide](../docs/api/README.md) or [focused examples](../examples/README.md). Run the commands below from the repository root. Use `-h` on any tool for its full flag list.

## Inspect an SCD offline

```bash
go run ./cmd/scdinspect -file examples/scd/breaker/bay.scd
go run ./cmd/scdinspect -file examples/scd/breaker/bay.scd -ied BayIED -ap S1 -match XCBR1.Pos
```

Use `-ref BayIEDCTRL/XCBR1.Pos.Oper -fc CO` to resolve one exact point. Use `-ied StationHMI -ap S1 -inputs` to list the receiver's GOOSE bindings. [SCD API and paths](../docs/api/scl.md)

## MMS client and server

Start the server:

```bash
go run ./cmd/mms-server -address 127.0.0.1:8102
```

In another terminal, read and write the sample integer:

```bash
go run ./cmd/mms-client -address 127.0.0.1:8102 -item int -write-int 99
```

The client accepts `-domain`, `-item`, `-write-int`, and `-pdu`. The server exposes the sample model defined in [internal/models/mms](internal/models/mms/model.go). Use the device's actual domain, item, and listening address when connecting to another peer.

## GOOSE/SV offline frames

Write and decode complete Ethernet frames without opening a NIC:

```bash
go run ./cmd/goose-publisher -output-frame _local/frames/goose.bin
go run ./cmd/goose-subscriber -input-frame _local/frames/goose.bin
go run ./cmd/sv-publisher -output-frame _local/frames/sv.bin
go run ./cmd/sv-subscriber -input-frame _local/frames/sv.bin
```

## Use a real interface

List available interfaces with `go run ./cmd/sv-subscriber -list`. Replace `eth0` with the selected interface. Start the subscriber first, then the publisher in a second terminal:

```bash
go run ./cmd/sv-subscriber -interface eth0 -vlan 123 -count 3 -duration 5s
go run ./cmd/sv-publisher -interface eth0 -vlan 123 -interval 20ms -duration 1s
```

GOOSE uses the corresponding `goose-subscriber` / `goose-publisher` tools and `-min` / `-max` retransmission intervals. Relevant flags:

| Flag | Behavior |
|---|---|
| `-vlan -1` | Publish untagged frames or restrict reception to untagged frames |
| Subscriber default `-vlan -2` | Accept tagged and untagged frames |
| `-promiscuous` | Explicitly enable promiscuous reception when a Windows interface does not deliver the multicast group |
| `-capture <path>` | Save a PCAP capture |
| `-count <n>` | Require the requested count before the deadline; insufficient reception exits with an error |
| SV `-sync 0` | Mark samples as unsynchronized, the default |

See [platform requirements and permissions](../docs/platforms.md). The build scripts produce all seven tools under `_local/bin/<os>-<arch>/`. `cmd/internal` holds CLI, capture, and sample-model helpers; applications should import the public protocol packages.
