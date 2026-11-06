# Code structure and data flow

English | [简体中文](architecture.zh-CN.md)

The library is split by protocol. Applications can import and use MMS or a frame codec directly without creating a complete IEC 61850 device model.

| Layer | Package | Dependencies |
|---|---|---|
| Shared data | `data` | Internal BER helpers |
| MMS | `mms` | `data`, `internal/iso`, `internal/ber` |
| Layer 2 GOOSE | `goose` | `data`, `internal/ber` |
| Layer 2 SV | `sv` | `internal/ber`; raw application layout for seqData |
| Engineered device model | `scl` | `encoding/xml`, `mms`, `goose` and `data` |
| Optional Ethernet access | `ethernet` | Standard library and native operating-system interfaces |
| Application examples | `examples` | Public library and example-only helpers |

`internal/iso` implements TPKT, COTP, Session, Presentation, and ACSE. `mms` uses it to exchange MMS PDUs. The client dispatches concurrent responses by InvokeID; the server maps requests to a `Store`. `Model` is a ready-to-use, concurrency-safe in-memory store.

GOOSE/SV `Frame` types hold Ethernet fields; PDU/ASDU types hold protocol content. Encoding and decoding handle wire representation. Publishers and subscribers manage transmission and reception state. `FrameIO` separates those state machines from the network interface. Injectable clocks and timers support deterministic tests and application scheduling.

`data.Value` is immutable and `TypeSpec` declares constraints. Frames, PDUs, and ASDUs contain mutable slices or pointers; use `Clone` for defensive copies. SV `SampleData` is an explicitly mutable byte buffer accessed by offsets. `Publisher.UpdateData` copies the supplied payload.

Applications close their own links, Ethernet connections, and listeners according to each API's ownership rules. MMS requests take contexts. Examples cancel and wait for background work before returning. GOOSE/SV event queues are bounded and report overflow explicitly.

`cmd/` contains network tools, with argument helpers and sample models in `cmd/internal/`. Each `examples/` entry demonstrates one API task; `examples/internal/` supplies a local MMS peer and an in-memory link. Applications use public packages directly.

Go unit tests are kept with their packages. Reference C code, cross-language tests, and standards are stored in ignored `_local/` directories; they are not production dependencies and are outside the root module's `go test ./...` scope.

[API guide](api/README.md) · [Examples](../examples/README.md) · [Development](development.md)
