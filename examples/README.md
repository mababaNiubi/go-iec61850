# Examples by application task

English | [简体中文](README.zh-CN.md)

Each directory demonstrates one task. Open its `main.go` to follow the API calls; use the English guide beside it for the steps and expected output. The protocol examples also contain Chinese step comments. The [API guide](../docs/api/README.md) explains parameters and return values in detail.

Run all commands from the repository root.

| Task | Source | Command |
|---|---|---|
| Control an IED, inspect acknowledgement and subscribe to URCB | [Device client](ied/control-report/README.md) | `go run ./examples/ied/control-report` |
| Decode engineered SV samples without hard-coded offsets | [SCL/SV](sv/scl/README.md) | `go run ./examples/sv/scl` |
| Integrate a modeled device and a station from SCD | [Breaker model, driver, and monitor](scd/breaker/README.md) | `go run ./examples/scd/breaker` |
| Connect, read an integer, write, and read it back | [MMS read/write](mms/read-write/main.go) | `go run ./examples/mms/read-write` |
| Batch access, partial failures, and array/structure selection | [MMS batch reads](mms/read-batch/main.go) | `go run ./examples/mms/read-batch` |
| Discover domains, names, and declared types | [MMS browsing](mms/browse/main.go) | `go run ./examples/mms/browse` |
| Define, write, read, and delete an AA named list | [MMS named lists](mms/named-list/main.go) | `go run ./examples/mms/named-list` |
| Send a server-initiated report and receive it on the client | [MMS reports](mms/report/main.go) | `go run ./examples/mms/report` |
| Publish false then true and receive both states | [GOOSE](goose/publish-subscribe/main.go) | `go run ./examples/goose/publish-subscribe` |
| Update a measurement and send two consecutive samples | [SV](sv/publish-subscribe/main.go) | `go run ./examples/sv/publish-subscribe` |
| Place integers, floats, Quality, and UTC at explicit offsets | [SV byte layout](sv/data-layout/main.go) | `go run ./examples/sv/data-layout` |

The four MMS examples for reading, batching, browsing, and named lists start and close a temporary loopback peer by default. To use the sample command-line server instead, start `go run ./cmd/iedlab protocol mms-server`, then add `-address 127.0.0.1:8102` to the example command. These examples expect its `sample` variables. The report example includes both endpoints locally.

GOOSE/SV examples use an in-memory `FrameIO` to exercise frame encoding, decoding, and state objects without NIC permissions. For real Ethernet, use the `ethernet.Open` snippets in the API guide or the [command-line tools](../cmd/README.md).

## Example helpers

[internal/mmsdemo/server.go](internal/mmsdemo/server.go) only supplies the local MMS peer; [internal/memory/link.go](internal/memory/link.go) only supplies the in-memory link. Applications can keep the calls after `Dial`, or replace the memory link with an Ethernet `Conn`.

Protocol boundary cases and error paths live in package `*_test.go` files. Examples focus on how an application uses the API.
