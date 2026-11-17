# API quick start

English | [简体中文](quickstart.zh-CN.md)

[Project overview](../../README.md) · [API index](README.md) · [MMS reference guide](mms.md)

This tutorial connects a Go client to a local MMS server, writes an integer and
reads it back. It needs Go 1.24+, two terminals and no physical IED, Ethernet
capture driver or C library. Commands identify whether they run in the SDK
checkout or your own application module.

## 1. Start the sample server

In terminal A, clone the SDK if you do not already have it:

```sh
git clone https://github.com/mababaNiubi/go-iec61850.git
cd go-iec61850
go run ./cmd/iedlab protocol mms-server -address 127.0.0.1:8102
```

Keep this terminal running. The fixture exposes a writable integer named
`sample/int`. If port 8102 is occupied, choose another port and change both
server and client addresses. To try an automatically managed local peer instead,
run `go run ./examples/mms/read-write` from this checkout.

## 2. Create an application module

In terminal B, outside the SDK checkout, create a new empty directory:

```sh
mkdir iec61850-demo
cd iec61850-demo
go mod init example.com/iec61850-demo
go get github.com/mababaNiubi/go-iec61850@v1.4.1
```

The application imports the published module. No `replace`, local SDK directory
or `go.work` is needed.

## 3. Write and run the client

Save the complete program as `main.go` in `iec61850-demo`:

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

From the same application directory:

```sh
go run .
```

Expected output:

```text
sample/int = 99
```

The process closes its association on success and error. Stop the sample server
with Ctrl+C when finished. This confirms a write/read exchange with the fixture;
equipment actuation requires a modeled control and independent feedback.

## 4. Understand the calls

| Call | Contract |
|---|---|
| `mms.Dial(ctx, address, cfg)` | Connect and negotiate at `host:port`. Its context bounds setup; subsequent operations use their own contexts. `mms.Config{}` selects bounded defaults. |
| `defer client.Close()` | Release the owned association on every return path. `Conclude(ctx)` optionally performs graceful release after pending requests finish. |
| `mms.DomainName(domain, item)` | Build an MMS object name. `sample` and `int` are identifiers in this fixture, not arbitrary local variable names. |
| `mms.Integer(99)` | Construct a signed protocol value. Choose the constructor matching the remote declaration; Go integers are not automatically converted on write. |
| `client.Write` / `client.Read` | Perform confirmed MMS services. Check errors; a timeout after submitting a write can leave its outcome uncertain. |
| `value.Int()` | Return an `int64`, or a kind-mismatch error. `Bool`, `Uint`, `Float`, `StringValue` and `Time` provide other checked conversions. |

The example shares one five-second deadline across the entire short workflow.
Long-running applications should create a bounded context for each operation.
The default MMS `RequestTimeout` is five seconds per confirmed request; see
`go doc github.com/mababaNiubi/go-iec61850/mms.Config` for limits.

## 5. Connect to an engineered IED

Use the actual address, domain and declarations supplied by the device/SCL.
A typical IEC reference `XCBR1.Pos.stVal` with FC `ST` maps to MMS item
`XCBR1$ST$Pos$stVal`; `ied.ObjectName(domain, fc, reference)` performs that mapping.
The fixture's writable `sample/int` is not an equipment-control mapping.

| Application task | Public entry point | Guide / runnable example |
|---|---|---|
| Discover online objects without SCL | `ied.Dial`, `Client.Discover` | [Discovery and snapshots](engineering.md#online-discovery-and-current-snapshots), [GoDoc examples](../../ied/example_test.go) |
| Read several configured values | `mms.Client.ReadMultiple` | [Batch reads](mms.md#read-several-variables), [example](../../examples/mms/read-batch/main.go) |
| Receive URCB/BRCB events | `ied.Client.SubscribeURCB`, `SubscribeBRCB` | [Reports and controls](ied.md), [replay](engineering.md#resume-buffered-history) |
| Read current dataset values | `Client.ReadDataSet`, `ReportSubscription.ReadSnapshot` | [Snapshot semantics](engineering.md#online-discovery-and-current-snapshots) |
| Operate equipment | `ied.Client.Operate` | [Controls](ied.md#connect-read-and-control), [example](../../examples/ied/control-report/main.go) |
| Bind acquisition and actuator code | `scl.ParseFile`, `System.Device`, `IED.Update`, `IED.OnControl` | [SCL guide](scl.md), [station application](../../examples/scd/station/main.go) |
| Use GOOSE or SV | `goose` / `sv` layouts, publishers and receivers | [GOOSE](goose.md), [SV](sv.md), [examples](../../examples/README.md) |

For batch reads, inspect both the request error and every `ReadResult.Error`.
For subscriptions, drain `Events()` promptly, inspect `Err()` after closure,
and stop subscriptions before closing the client. `ied.Client` owns the raw MMS
report channel; do not consume `client.MMS().Reports()` separately.

Current snapshots have no EntryID. Persist only the EntryID of a complete BRCB
report that your application has durably processed. Control acknowledgement
and enhanced termination do not replace physical ST feedback. These contracts
are detailed in the [API index](README.md#names-ownership-and-results).

## Troubleshooting

| Symptom | Check |
|---|---|
| Connection refused | Terminal A is still running and both programs use the same host/port. |
| Object nonexistent or access denied | Use the fixture's `sample/int`, or inspect the real device's names and permissions. |
| Kind/type mismatch | Read the declared type with `GetVariableAccessAttributes`; use matching constructors/getters. |
| Deadline exceeded | Check connectivity and ISO/ACSE parameters; confirm the outcome before retrying a submitted write. |
| No report events | Use an allocated, configured RCB and drain its queue. A successful subscription does not guarantee an immediate event. |
| No physical GOOSE/SV traffic | Check the selected NIC, permissions/Npcap, engineered MAC/APPID/VLAN and device publication settings. The MMS fixture does not publish these streams. |

For a reproducible problem, include the module version, OS/architecture,
operation/error and a minimal sanitized program in a [GitHub issue](https://github.com/mababaNiubi/go-iec61850/issues).
