# go-iec61850

English | [简体中文](README.zh-CN.md)

Native Go MMS clients and servers, Layer 2 GOOSE, and Sampled Values (SV). Requires Go 1.24 or later. The runtime library has no third-party Go dependencies and does not require cgo.

## Open the browser workbench

```sh
go run ./cmd/iedlab serve -http 127.0.0.1:8080
```

Open `http://127.0.0.1:8080`, load an SCL file or the sample, and select an IED. Browse logical nodes, edit local acquisition values, start an MMS/GOOSE simulator and connect the MMS client. The frontend is embedded in the Go binary. [Workbench guide](docs/workbench.md) · [简体中文](docs/workbench.zh-CN.md)

## Run a model-driven device and monitor

From this checkout, run `go run ./examples/scd/breaker` to see an SCD-defined breaker, MMS acquisition and control, and GOOSE feedback. Use `go run ./cmd/iedlab inspect -file internal/sample/bay.scd` to list its access points offline. SCD model integration is available in v1.1.0. [SCD API and common paths](docs/api/scl.md) · [Device and monitor example](examples/scd/breaker/README.md)

## Install

```bash
go get github.com/mababaNiubi/go-iec61850@v1.4.0
```

Import the package for the protocol you need, such as `github.com/mababaNiubi/go-iec61850/mms`.

## Connect to a device and read or write a variable

Start the sample peer in another terminal:

```bash
go run ./cmd/iedlab protocol mms-server -address 127.0.0.1:8102
```

Use the following calls inside a function that returns an error:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

client, err := mms.Dial(ctx, "127.0.0.1:8102", mms.Config{})
if err != nil { return err }
defer client.Close()

name := mms.DomainName("sample", "int")
if err := client.Write(ctx, name, mms.Integer(99)); err != nil { return err }
value, err := client.Read(ctx, name)
if err != nil { return err }
number, err := value.Int()
if err != nil { return err }
fmt.Println("sample/int =", number)

return client.Conclude(ctx)
```

Import `github.com/mababaNiubi/go-iec61850/mms` and the standard `context`, `time`, and `fmt` packages. See the [complete program, parameter descriptions, and error handling](docs/api/mms.md#connect-and-read-or-write-an-integer).

To try the same workflow with an automatically started local peer:

```bash
go run ./examples/mms/read-write
```

This example uses a temporary loopback server by default. Add `-address host:port` to connect to an existing peer that exposes the sample model.

## Find an API by task

| Task | Guide |
|---|---|
| Online discovery, current dataset reads, BRCB replay, engineering networks and station callbacks | [Engineering workflows](docs/api/engineering.md) |
| Device controls, URCB subscriptions, embedded simulation and SCL-defined SV layouts | [Device integration API](docs/api/ied.md) |
| Read/write, batch access, arrays and structures, browsing, named lists, reports, or a custom server | [MMS API](docs/api/mms.md) |
| Convert MMS values to Go integers, booleans, floats, strings, arrays, or time | [Values and types](docs/api/data.md) |
| Publish state changes, subscribe, and detect TAL expiry | [GOOSE API](docs/api/goose.md) |
| Publish/subscribe to samples, use multiple ASDUs, or define a byte layout | [SV API](docs/api/sv.md) |

[API guide](docs/api/README.md) · [Application examples](examples/README.md) · [Command-line tools](cmd/README.md) · [Release notes](CHANGELOG.md)

## Repository layout

- `examples/`: one application task per directory, with annotated source and a usage guide. MMS uses loopback by default; GOOSE/SV use an in-memory link.
- `cmd/iedlab/`: one application with `serve`, `simulate`, `inspect` and grouped `protocol` diagnostics.
- `internal/workbench/`: workspace state, HTTP API and embedded frontend; protocol lifetimes use the public `simulator` package.
- `internal/sample/`: one SCL fixture shared by the application and examples.
- `mms/`, `data/`, `goose/`, `sv/`, `ethernet/`, `scl/`, `ied/`, `simulator/`: the library packages applications import.
- `docs/api/`: practical API documentation. English is the default; each guide links to its Simplified Chinese version.
- `_local/`: ignored C references, interoperability fixtures, standards, reports, and build artifacts. These files are not distributed with Git.

See [architecture](docs/architecture.md) for package responsibilities.

## Build and development checks

```powershell
./scripts/build.ps1
./scripts/test.ps1 -Race -Examples
```

```bash
bash scripts/build.sh
bash scripts/test.sh --race --examples
```

The `iedlab` executable is written to `_local/bin/<os>-<arch>/`. Production builds use `CGO_ENABLED=0` and `-trimpath`. Real Ethernet access requires Npcap on Windows or raw socket permissions on Linux; see [platform support](docs/platforms.md). [Development](docs/development.md) describes the optional local C interoperability setup.

## Scope and license

Version 1.4.0 provides MMS, ordinary Layer 2 GOOSE/SV, engineered URCB/BRCB reporting and replay, immediate binary controls, SP/SG settings, bounded SCL arrays and nested alternate access. The [documented profiles](docs/api/scl.md) describe supported modeling. Full ACSI/SCL coverage, online GoCB/SVCB reconfiguration, R-GOOSE/R-SV, PTP, and IEC 62351 remain outside this scope. A version tag does not imply IEC conformance certification.

Start with the [engineering API](docs/api/engineering.md) and [station example](examples/scd/station/README.md). IEDLab's **Station sample → Start station** starts the engineered IEDs and exposes GOOSE/SV inputs in the browser. The same workspace runs headlessly with `iedlab simulate -station`.

Licensed under **GPL-3.0-or-later**. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
