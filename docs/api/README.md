# API guide

English | [简体中文](README.zh-CN.md)

Install the public SDK with Go 1.24 or later:

```sh
go get github.com/mababaNiubi/go-iec61850@v1.4.0
```

Import packages below `github.com/mababaNiubi/go-iec61850/`. Applications use public packages directly; IEDLab is an optional CLI/browser application built on the same APIs.

| Package | Use it for |
|---|---|
| `mms` | TCP associations, raw reads/writes, directory services and custom servers |
| `ied` | Online discovery, modeled controls, URCB/BRCB subscriptions, current dataset snapshots and settings |
| `scl` | Parse SCD/ICD/CID engineering declarations, resolve paths, inspect models and create device stores |
| `simulator` | Own a device runtime or a multi-IED software station; bind your acquisition/control logic |
| `data` | Immutable typed values, declarations, component selection and validation |
| `goose`, `sv` | Layer 2 publication, subscription, typed layouts and shared receivers |
| `ethernet` | Physical NIC frame transport on supported platforms |

## Choose a workflow

| Task | API documentation | Runnable source |
|---|---|---|
| Connect without an SCL file, discover services, read current datasets | [Online discovery and snapshots](engineering.md#online-discovery-and-current-snapshots) | [Executable GoDoc examples](../../ied/example_test.go) |
| Replay BRCB history and retain a durable EntryID | [Buffered history](engineering.md#resume-buffered-history) | [Station model](../../examples/scd/station/station.scd) |
| Inspect networks/ServerAt, compare revisions and bind scoped ExtRef inputs | [Engineering bindings](engineering.md#engineering-validation-and-application-bindings) | [Station application](../../examples/scd/station/main.go) |
| Read, edit/confirm or activate a setting group | [Settings](engineering.md#control-and-configure-equipment) | [Settings application](../../examples/ied/settings/main.go) |
| Control by ctlModel, subscribe to URCB, embed a simulator or import SV layouts | [Device API](ied.md) | [control-report](../../examples/ied/control-report/main.go), [SCL/SV](../../examples/sv/scl/main.go) |
| Use a browser workspace or headless CLI for IED debugging | [IEDLab workbench](../workbench.md) | [Application entry point](../../cmd/iedlab/main.go) |
| Load an SCD, find model paths, bind acquisition and controls, subscribe by ExtRef | [SCD model integration](scl.md) | [breaker IED and station](../../examples/scd/breaker/README.md) |
| Connect and read or write a variable | [MMS: connect and read/write](mms.md#connect-and-read-or-write-an-integer) | [read-write](../../examples/mms/read-write/main.go) |
| Read several variables and handle partial failures | [MMS: batch reads](mms.md#read-several-variables) | [read-batch](../../examples/mms/read-batch/main.go) |
| Discover device names and declared types | [MMS: browse](mms.md#discover-domains-variable-names-and-types) | [browse](../../examples/mms/browse/main.go) |
| Access an array element or structure member | [MMS: partial access](mms.md#read-an-array-element-or-structure-member) | [read-batch](../../examples/mms/read-batch/main.go) |
| Group variables into a named list | [MMS: named lists](mms.md#read-and-write-a-named-variable-list) | [named-list](../../examples/mms/named-list/main.go) |
| Receive a server-initiated report | [MMS: InformationReport](mms.md#receive-and-send-informationreport) | [report](../../examples/mms/report/main.go) |
| Create an MMS server | [MMS: server](mms.md#create-an-mms-server) | [local peer](../../examples/internal/mmsdemo/server.go) |
| Convert an MMS Value to a Go value | [Values and types](data.md) | [read-write](../../examples/mms/read-write/main.go) |
| Publish and subscribe to state changes | [GOOSE API](goose.md) | [publish-subscribe](../../examples/goose/publish-subscribe/main.go) |
| Publish, receive, or decode sampled values | [SV API](sv.md) | [publish-subscribe](../../examples/sv/publish-subscribe/main.go) |
| Define the SV payload byte layout | [SV: custom layout](sv.md#define-a-custom-sample-layout) | [data-layout](../../examples/sv/data-layout/main.go) |


## Discover an IED and read its current dataset

The following complete function needs only an address. It uses the first online dataset for demonstration; a monitoring application should select its intended dataset from the directory. Run a local fixture with `go run ./examples/scd/station` or use an actual IED address.

```go
import (
    "context"
    "fmt"
    "time"

    "github.com/mababaNiubi/go-iec61850/ied"
)

func inspect(address string) error {
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    client, err := ied.Dial(ctx, address, ied.Config{})
    if err != nil { return err }
    defer client.Close()

    directory, err := client.Discover(ctx, ied.DiscoveryOptions{MaxObjects: 4096})
    if err != nil { return err }
    if directory.Truncated || len(directory.Diagnostics) != 0 {
        fmt.Println("Incomplete discovery:", directory.Truncated, directory.Diagnostics)
    }
    if len(directory.DataSets) == 0 { return fmt.Errorf("no online dataset found") }
    snapshot, err := client.ReadDataSet(ctx, directory.DataSets[0].Name)
    if err != nil { return err }
    leaves, err := snapshot.Leaves()
    if err != nil { return err }
    for _, leaf := range leaves {
        // Keep Variable.Selection when identifying array elements.
        fmt.Println(leaf.Variable.Name, leaf.Variable.Selection, leaf.Value)
    }
    return nil
}
```

Discovery returns declarations, not process values. Check `Diagnostics` and `Truncated`. `ReadDataSet` reads online member types and then values in one confirmed MMS Read. It returns an error if any member fails. Call the typed value accessors (`Bool`, `Int`, `Uint`, `Float`, `Time`) and handle conversion errors. [Compiled and executed examples](../../ied/example_test.go) also appear in `go doc`/pkg.go.dev.

## Add your device logic to an SCL model

1. `scl.ParseFile(path)` loads the engineering file. `system.Project()` and `system.Catalog(iedName, apName)` inspect the supported declarations and diagnostics without opening a connection.
2. `system.Device(iedName, apName)` builds an independent device store. Resolve paths through `model.Lookup`/`Signal`; use `Signal.Sample(value, quality, timestamp)` and a single `model.Update(ctx, changes...)` to commit a coherent acquisition.
3. Register `model.OnControl` before serving. Execute your driver in the callback and update ST feedback from the actual acquisition. `OnOperate` remains the direct-normal binary-control interface.
4. `simulator.Start(ctx, model, options)` owns one device. `simulator.StartStation(ctx, system, options)` wires multiple devices on separate SubNetwork buses. Supply custom models/sources and `OnSample`/`OnGOOSE` logic through `StationOptions`.
5. A monitoring application uses `ied.Dial`, reports and current reads independently. With engineering available, `catalog.CheckOnline(ctx, client.MMS())` compares the selected online types and datasets before reusing a mapping.

Start with the [annotated station application](../../examples/scd/station/main.go) and [step-by-step engineering guide](engineering.md). The parser's supported semantic diagnostics do not replace edition-matching XSD or full NSD validation.

## Names, ownership and results

| Contract | What the caller must do |
|---|---|
| MMS name | Use `mms.DomainName("BayIEDCTRL", "XCBR1$ST$Pos$stVal")` for a wire name, or `ied.ObjectName("BayIEDCTRL", "ST", "XCBR1.Pos.stVal")` for a relative IEC path. Domain and FC come from the model/online device. |
| Model lookup | `model.Lookup("CTRL", "XCBR1", "ST", "Pos.stVal")` resolves the engineered LD alias and declaration; avoid constructing domain strings by concatenation. |
| Request deadlines | `Dial` context bounds setup; give each later operation its own context. `mms.Config.RequestTimeout` defaults to 5 seconds per confirmed request. |
| Resource ownership | Defer `Close` after successful construction. An `ied.Client` owns its MMS report dispatcher; do not read `client.MMS().Reports()` yourself. Stop subscriptions before closing the client. |
| Subscription lifetime | `SubscribeURCB/BRCB` context bounds setup only. Consume `Events()` promptly; after channel close inspect `Err()`. Use `Stop(ctx)`, `Close` or client close to release it. |
| Batch failure | `ReadMultiple`/`ReadNamedList` have an overall error and an ordered `ReadResult.Error` for each member. A successful transaction can contain failed members. |
| Current values vs history | `ReadDataSet`/`ReadSnapshot` have no EntryID. Save only a complete, durably processed BRCB report's EntryID as the replay cursor. GI has no request correlation ID. |
| Control outcome | Inspect `Selected`, `Acknowledged`, `Terminated` even on an error. Read ST separately for physical feedback. Never automatically replay an uncertain operation. |
| Quality and time | Retain q, source t and local receive time separately. A last value may remain present after expiry; inspect `InputReading.Valid`/`State`. Scaling and quality acceptance are application policy. |
| Callbacks | Receiver/station callbacks run synchronously on workers and may run concurrently across streams. Return promptly, protect shared application state and initiate shutdown from the owner. |
| Layer 2 transport | GOOSE/SV need a NIC or `FrameIO`; examples use memory transport. The software station does not provide a synchronized physical sampling clock. |

## Look up the exact public signature

From this module or a consuming Go module:

```sh
go doc github.com/mababaNiubi/go-iec61850/ied.Client.Discover
go doc github.com/mababaNiubi/go-iec61850/ied.ReportOptions
go doc github.com/mababaNiubi/go-iec61850/simulator.StationOptions
go doc -all github.com/mababaNiubi/go-iec61850/scl
go test ./ied -run Example -v
```

GoDoc comments describe defaults, units, allowed ranges, ownership and error semantics next to the public declaration. For tools, use those signatures and comments together with these workflow guides. `internal/` packages are application implementation details.

[Running the examples](../../examples/README.md) · [Workbench](../workbench.md) · [Command-line tools](../../cmd/README.md)
