# Connect a device or station monitor from an SCD

English | [简体中文](scl.zh-CN.md)

Run the [breaker example](../../examples/scd/breaker/README.md) from the repository root. Its [SCD](../../internal/sample/bay.scd) describes a bay controller, a station monitor and the station bus. Both applications use the same engineered names and dataset order.

```sh
go run ./examples/scd/breaker
```

The station sees an open breaker over GOOSE and reads 0 W over MMS. Its first close command is blocked by the simulated interlock. After the permissive changes, it closes the breaker, reads 1250 W and receives closed feedback. An open command returns both channels to the initial state.

## Find your access point and paths

```sh
go run ./cmd/iedlab inspect -file internal/sample/bay.scd
go run ./cmd/iedlab inspect -file internal/sample/bay.scd -ied BayIED -ap S1 -match XCBR1.Pos
go run ./cmd/iedlab inspect -file internal/sample/bay.scd -ied BayIED -ap S1 -ref BayIEDCTRL/MMXU1.TotW.mag.f -fc MX
go run ./cmd/iedlab inspect -file internal/sample/bay.scd -ied StationHMI -ap S1 -inputs
```

The commands list access points and their server role, print matching points and communication settings, resolve one exact name and type, and discover the station's GOOSE inputs. All work offline.

| Application signal | SCL path within LD instance `CTRL` | FC | MMS item in domain `BayIEDCTRL` |
|---|---|---|---|
| Active power in watts | `MMXU1.TotW.mag.f` | MX | `MMXU1$MX$TotW$mag$f` |
| Measurement quality / time | `MMXU1.TotW.q` / `MMXU1.TotW.t` | MX | `MMXU1$MX$TotW$q` / `MMXU1$MX$TotW$t` |
| Actual breaker position | `XCBR1.Pos.stVal` | ST | `XCBR1$ST$Pos$stVal` |
| Position quality / last change time | `XCBR1.Pos.q` / `XCBR1.Pos.t` | ST | `XCBR1$ST$Pos$q` / `XCBR1$ST$Pos$t` |
| Close blocked / operation count | `XCBR1.BlkCls.stVal` / `XCBR1.OpCnt.stVal` | ST | `XCBR1$ST$BlkCls$stVal` / `XCBR1$ST$OpCnt$stVal` |
| Control model | `XCBR1.Pos.ctlModel` | CF | `XCBR1$CF$Pos$ctlModel` |
| Whole direct-control command | `XCBR1.Pos.Oper` | CO | `XCBR1$CO$Pos$Oper` |
| Ordered position dataset | `LLN0 / dsPos` | dataset | `LLN0$dsPos` |

`BayIEDCTRL` comes from the IED name `BayIED` and LD instance `CTRL`. An explicit `LDevice.ldName` takes precedence. An LN name includes its prefix, class and instance. `Resolve` accepts the full reference and FC. `Lookup` accepts the LD instance, LN, FC and dot-separated data path. Both return an error for a path or FC absent from the selected model.

## Where to edit the SCD

| Change | SCL element path |
|---|---|
| Device identity, LD name or LN instance | `IED/AccessPoint/Server/LDevice/LN` (or `LN0`) |
| Point and its wire type | `DataTypeTemplates/LNodeType/DO` → `DOType/DA` (or `SDO`) → `DAType/BDA` or `EnumType` |
| One instance's initial value, including control model | `LN/DOI/DAI/Val` or nested `SDI/DAI/Val` |
| Dataset membership and order | `LN0/DataSet/FCDA` |
| Published control and Ethernet settings | `LN0/GSEControl` matched to `Communication/SubNetwork/ConnectedAP/GSE` by IED, AP, LD instance and control block name |
| Receiving application input | `LN0/Inputs/ExtRef` or `LN/Inputs/ExtRef`, using `intAddr`, source control fields and the source data reference |

## Read a point from an existing device

The snippet imports `context` and the `mms` and `scl` packages under `github.com/mababaNiubi/go-iec61850/`. Pass a context with an appropriate deadline. Install the package with `go get github.com/mababaNiubi/go-iec61850/scl@v1.1.0`.

```go
func readWatts(ctx context.Context, path string) (float64, error) {
    system, err := scl.ParseFile(path)
    if err != nil { return 0, err }
    ied, err := system.Device("BayIED", "S1")
    if err != nil { return 0, err }
    name, err := ied.Lookup("CTRL", "MMXU1", "MX", "TotW.mag.f")
    if err != nil { return 0, err }

    client, err := mms.Dial(ctx, ied.MMSAddress(), ied.ClientConfig())
    if err != nil { return 0, err }
    defer client.Close()
    value, err := client.Read(ctx, name)
    if err != nil { return 0, err }
    return value.Float()
}
```

In a long-running monitor, parse once and reuse the client and the resolved names. `MMSAddress` uses the configured IP/DNS and TCP port (default 102). Supply your endpoint when the SCD omits it. `ClientConfig` sets the called T/S/P selectors and the AP title/AE qualifier when present. Override local calling parameters if the peer requires them. For a batch read, inspect each `ReadResult.Error`. See the [station loop](../../examples/scd/breaker/monitor.go).

## Connect acquisition and control logic

On the IED, resolve the signal names once. Bind the group with `power, err := ied.Signal("CTRL", "MMXU1", "MX", "TotW", "mag.f")`. Have the driver acquire an actual value, its quality and timestamp. The following function commits one coherent measurement from the driver.

```go
func updatePower(ctx context.Context, ied *scl.IED, power scl.Signal,
    sample float32, quality mms.Value, acquired time.Time) error {
    return ied.Update(ctx, power.Sample(mms.Float32(sample), quality, mms.UTC(acquired))...)
}
```

A typical good quality value is `mms.BitString([]byte{0, 0}, 13)`. Preserve the driver's quality and time when available. `Update` validates the whole batch before committing it. Network clients have read-only access to ST/MX. The example keeps the position timestamp at the last physical state change while measurements get new acquisition times.

Register a control callback on the resolved CO `Pos.Oper` before serving. The callback receives a decoded `scl.Command` with the Boolean request, origin, control number, time, Test and Check flags. It applies device interlocks, calls the actuator and acquires actual feedback. An MMS write to `Pos.stVal` or `Oper.ctlVal` is denied. With asynchronous hardware, publish the feedback once the driver observes it. Confirm the new position from the driver's feedback. See [process.go](../../examples/scd/breaker/process.go) for the replaceable `processIO` interface and [main.go](../../examples/scd/breaker/main.go) for server lifecycle.

On the station, resolve the whole CO object and issue an immediate direct control. Choose a control number for each command and use the origin assigned to your station. Confirm feedback by an ST read or a GOOSE event.

```go
func closeBreaker(ctx context.Context, client *mms.Client, oper mms.ObjectName, number uint8) error {
    cmd := scl.Command{
        Value: true, Number: number, Time: time.Now(),
        OriginCategory: 2, OriginID: []byte("StationHMI"), InterlockCheck: true,
    }
    value, err := cmd.MMSValue()
    if err != nil { return err }
    return client.Write(ctx, oper, value)
}
```

## Subscribe to the engineered GOOSE input

The publishing IED selects `ied.Stream("CTRL", "gcbPos")` and runs `stream.Run(ctx, link, sourceMAC)`. The SCD supplies `BayIEDCTRL/LLN0$GO$gcbPos`, dataset `BayIEDCTRL/LLN0$dsPos`, confRev 1, destination `01-0c-cd-01-00-01`, APPID `0x1001`, VID 123 and PCP 4. A real link uses `ethernet.Open` and `conn.MAC()`. The publisher retransmits and refreshes its dataset when acquisition changes. If the link falls behind 64 distinct queued dataset snapshots, `Run` returns `scl.ErrPublicationOverflow`.

The monitor looks up `input, err := ied.Input("StationHMI", "S1", "breakerPosition")`. This resolves the receiver's `ExtRef.intAddr`, source GSEControl and FCDA membership. Create the subscriber with `input.Stream.NewSubscriber()` and call `sub.Run(ctx, link)`. Consume `sub.Events()` and also observe the return from `Run`. On a valid event, `input.Value(event)` selects the configured entry or the member of a whole-DO entry. Decode position with `scl.ReadPosition`. Mark the displayed value stale on `goose.Expired` and restore it on a valid recovery. The example waits for the network loops to exit before closing the link.

## Parser and lookup methods

| Method | Use |
|---|---|
| `scl.ParseFile(path)` / `scl.Parse(reader)` | Load an SCL file, embedded bytes, or an uploaded reader |
| `system.AccessPoints()` / `system.Device(ied, ap)` | List access points and instantiate a model on an AP with a Server |
| `ied.Points()` / `ied.Resolve(ref, fc)` / `ied.Lookup(ld, ln, fc, path)` | Browse or resolve checked MMS names |
| `ied.Signal(ld, ln, fc, object, member)` / `signal.Sample(value, q, t)` | Bind one signal's value, quality and time, then prepare its atomic acquisition |
| `ied.Type(ctx, name)` | Read the declared MMS type, including structure members |
| `ied.DataSets()` / `ied.DataSet(ld, ln, set)` | Obtain static MMS list names and ordered FCDA members for `ReadNamedList` |
| `ied.Streams()` / `ied.Stream(ld, cb)` / `stream.Info()` | Inspect GSEControl references, address, VLAN, timing and dataset order |
| `system.GOOSEInputs(receiver, ap)` | Discover ExtRef names, publishing IEDs, source control blocks and data paths, including client-only access points |
| `ied.Input(receiver, ap, intAddr)` | Bind an explicit GOOSE ExtRef to its source and dataset slot |
| `ied.NewServer(cfg)` / `ied.OnOperate(name, handler)` / `ied.Update(ctx, changes...)` | Serve the model, attach a binary control, and commit process feedback |

The runtime supports the selected server's LDevices, LN prefixes, ordinary non-array DO/SDO/DA/BDA templates, nested structures, enums, instance values, explicit FCDA targets (whole DO or attribute) and static ordinary Layer 2 GOOSE. The control profile is immediate SPC/DPC direct-with-normal-security with a six-field Oper. Arrays, setting groups, SBO/enhanced control, RCB reporting and online GoCB reconfiguration are outside this profile. Other sections such as substation topology are descriptive. The parser has a 16 MiB input limit and validates the modeled references. The runtime uses signed eight-bit enumerations and leaves unspecified initial enum values at zero. For full schema validation, use the XSD matching the SCD edition.

The fixture was validated against the IEC 61850-6 SCL 2007B4 XSD. SCL structure follows [IEC 61850-6 Ed. 2.1](https://webstore.iec.ch/en/publication/63319). The MMS/GOOSE mapping follows [IEC 61850-8-1](https://webstore.iec.ch/en/publication/66585). The reference implementation's [control services](https://support.mz-automation.de/doc/libiec61850/c/latest/group__IEC61850__CLIENT__CONTROL.html) provide a comparison for immediate direct control.
