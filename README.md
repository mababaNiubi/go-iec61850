# go-iec61850

Native Go libraries for IEC 61850 MMS, Layer 2 GOOSE and Sampled Values, with SCL model integration and the IEDLab browser workbench.

English | [简体中文](README.zh-CN.md)

[Quick start](docs/api/quickstart.md) · [API guide](docs/api/README.md) · [Go reference](https://pkg.go.dev/github.com/mababaNiubi/go-iec61850) · [Releases](https://github.com/mababaNiubi/go-iec61850/releases) · [Changelog](CHANGELOG.md)

Use the SDK to connect monitoring applications and device drivers, build model-backed IED simulators, or publish and receive engineered Ethernet streams. IEDLab provides a CLI and an embedded browser interface using the same public packages.

## Overview

The project has two entry points: a reusable **Go SDK** for applications and
**IEDLab**, a standalone debugging and simulation tool. Applications can import
the SDK without running the workbench or depending on its internal packages.

Typical uses include SCADA/gateway acquisition and control, model-backed device
simulators, and GOOSE/SV integration testing. SCL engineering declarations bind
logical devices, points, datasets and network streams to checked protocol types.
Your application supplies equipment drivers, calculations, scaling, quality
acceptance, retries and durable storage.

Key characteristics:

- **Native Go:** no C runtime library, cgo or third-party Go module dependencies.
- **Typed APIs:** checked accessors preserve signedness, 64-bit integers, float
  wire widths and raw quality/time representations.
- **Model-driven integration:** resolve engineered paths and ordered datasets;
  attach acquisition and actuator logic through public callbacks.
- **Explicit concurrency and ownership:** request contexts, owned runtimes,
  bounded report queues and shared Ethernet receivers support long-lived services.
- **Local development:** in-memory Ethernet links and loopback MMS peers let you
  exercise applications before connecting physical equipment.

## Features and scope

| Area | Available capabilities |
|---|---|
| MMS | Native Go client/server, browsing, typed read/write, batch and alternate access, named variable lists |
| IED client | Online discovery, immediate controls, URCB/BRCB subscriptions and replay, current dataset reads, setting groups |
| SCL | Supported SCD/ICD/CID models, typed point catalogs, datasets, equipment associations, SubNetworks/ServerAt and ExtRef resolution |
| GOOSE | Layer 2 publication/subscription, typed dataset layouts, retransmission and TAL expiry |
| SV | Layer 2 publication/subscription, multiple ASDUs, SCL-derived layouts and checked field access |
| Simulation | Single-device runtimes, multi-IED software stations, custom acquisition and actuator callbacks |
| IEDLab | SCL import, model browsing, simulation, online reports/control/settings and engineering comparison |

The runtime library has no third-party Go module dependencies and requires no cgo. Supported profiles and limits are described in the [SCL guide](docs/api/scl.md) and [platform notes](docs/platforms.md). Full ACSI/SCL coverage, online GoCB/SVCB reconfiguration, R-GOOSE/R-SV, PTP and IEC 62351 are outside the current scope. Releases do not imply IEC conformance certification.

## Requirements

- Go **1.24 or later** for the SDK and source builds.
- MMS uses TCP. Loopback examples need no NIC configuration.
- Physical GOOSE/SV access requires Linux raw-socket permissions or Windows amd64 with Npcap. Memory-link examples need neither. See [platform support](docs/platforms.md).

## Install

Add the SDK to an existing Go module:

```sh
go get github.com/mababaNiubi/go-iec61850@v1.4.0
```

Import the public package you need, such as `github.com/mababaNiubi/go-iec61850/mms` or `ied`. See the [package guide](docs/api/README.md#packages).

For IEDLab without a Go toolchain, download a Windows amd64 or Linux amd64 archive from [v1.4.0](https://github.com/mababaNiubi/go-iec61850/releases/tag/v1.4.0) and verify it against `SHA256SUMS.txt`. Each archive includes startup instructions and SCD models.

## Quick start

### Try a local client and server

```sh
git clone https://github.com/mababaNiubi/go-iec61850.git
cd go-iec61850
go run ./examples/mms/read-write
```

The example starts its own temporary loopback server and prints `sample/int: 42 -> 99`. It does not require an IED or physical Ethernet adapter.

### Use the API

From the repository root, keep the sample server running in another terminal:

```sh
go run ./cmd/iedlab protocol mms-server -address 127.0.0.1:8102
```

Save this program as `main.go` in a Go module with the SDK installed:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// This deadline bounds the entire short demonstration.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := mms.Dial(ctx, "127.0.0.1:8102", mms.Config{})
	if err != nil {
		return err
	}
	defer client.Close()

	name := mms.DomainName("sample", "int")
	if err := client.Write(ctx, name, mms.Integer(99)); err != nil {
		return err
	}
	value, err := client.Read(ctx, name)
	if err != nil {
		return err
	}
	number, err := value.Int()
	if err != nil {
		return err
	}
	fmt.Printf("sample/int = %d\n", number)
	return nil
}
```

Run `go run .`; expected output is `sample/int = 99`. This writes a variable exposed by the sample server. For an actual IED, select its declared domain, FC, type and access permissions. Use `ied` modeled controls for equipment operations and collect independent ST feedback.

The [API quick start](docs/api/quickstart.md) includes module setup, parameter explanations, common errors and next steps.

## IEDLab workbench

From the repository root:

```sh
go run ./cmd/iedlab serve -http 127.0.0.1:8080
```

Open `http://127.0.0.1:8080`. Choose **Station sample → Start station** to run the sample IEDs on software GOOSE/SV buses. Import your own SCL to inspect models or configure a simulator. The frontend is embedded in the executable.

[Workbench guide](docs/workbench.md) · [CLI reference](cmd/README.md) · [Complete station example](examples/scd/station/README.md)

## Documentation and examples

| Goal | Start here |
|---|---|
| Connect, read/write and understand lifetimes | [API quick start](docs/api/quickstart.md) |
| Find public packages and task-specific APIs | [API guide](docs/api/README.md) |
| Import a model and bind acquisition/control logic | [SCL integration](docs/api/scl.md) |
| Build monitoring with reports, replay and current reads | [Engineering workflows](docs/api/engineering.md) |
| Run and adapt a complete application | [Examples](examples/README.md) |
| Understand implementation boundaries | [Architecture](docs/architecture.md) |

## Development and contributing

Run the standard checks from the repository root:

```sh
go test ./... -count=1 -timeout=120s
go vet ./...
```

Use `go test -race ./...` where the race detector and a C compiler are available. Production builds can use `CGO_ENABLED=0`. [Development instructions](docs/development.md) cover builds, examples and optional independent C interoperability checks.

Report reproducible problems through [GitHub Issues](https://github.com/mababaNiubi/go-iec61850/issues). Include the version, OS/architecture, expected/actual behavior and a minimal sanitized model or program. For pull requests, explain the behavior change, add a focused regression when appropriate, and run the relevant checks. Exclude device credentials, private engineering files, captures and downloaded standards.

The public packages live in `mms/`, `ied/`, `scl/`, `simulator/`, `data/`, `goose/`, `sv/` and `ethernet/`. `cmd/iedlab/` is the application; `internal/` is its implementation. Ignored `_local/` holds references, standards and generated artifacts and is not required for ordinary builds.

## License

**GPL-3.0-or-later**. See [LICENSE](LICENSE) and [NOTICE](NOTICE) for license terms and upstream attribution. Reference standards retain their publishers' copyright and are not distributed as project source.
