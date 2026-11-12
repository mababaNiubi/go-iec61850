# Engineering a device and station monitor

[中文](engineering.zh-CN.md)

[Point selection](#select-points-by-primary-equipment) · [Reporting](#start-an-engineered-reporting-server) · [BRCB resume](#resume-buffered-history) · [Controls/settings](#control-and-configure-equipment) · [Station](#run-the-complete-station) · [Online discovery/current reads](#online-discovery-and-current-snapshots) · [Networks/bindings](#engineering-validation-and-application-bindings)


Run the complete station example from the repository root.

```sh
go run ./examples/scd/station
go run ./examples/ied/settings
```

The station uses one SCD for MUA, BayIED and StationHMI. MUA publishes encoded
SV frames. BayIED resolves its SMV ExtRef inputs, calculates three-phase active
power and exposes MMXU1.TotW over MMS. An MMS operation changes the virtual
breaker, produces a URCB report and publishes the position to the HMI's GOOSE
ExtRef input. Replace the waveform and virtual actuator callbacks with your
acquisition and equipment drivers.

## Select points by primary equipment

```go
system, err := scl.ParseFile("examples/scd/station/station.scd")
if err != nil { return err }
catalog, err := system.Catalog("BayIED", "S1")
if err != nil { return err }
for _, point := range catalog.Points {
    for _, equipment := range point.Equipment {
        if equipment == "Demo/E1/Q01/Q0" {
            fmt.Println(point.Reference, point.FC, point.Type,
                point.Quality, point.Timestamp)
        }
    }
}
```

`System.Equipment` preserves the station/voltage-level/bay/equipment path and
its LNode references. Catalog points retain CDC, SCL basic type, enum labels,
description, declared unit structure, and the nearest compatible q/t siblings.
Each unsupported DO produces a diagnostic while other DOs remain inspectable.
`System.Device` constructs a complete supported runtime and returns an error
when that selected device requires an unsupported declaration.

Use `catalog.CheckOnline(ctx, client.MMS())` before accepting an engineering
mapping. It compares online types and static dataset member order, returning
all discrepancies. `scl.CheckDataSet` checks a selected dataset independently.
With only an IED address, use [client.Discover](#online-discovery-and-current-snapshots)
for bounded online types, datasets, report blocks and setting groups.

## Start an engineered reporting server

```go
ctx := context.Background()
model, err := system.Device("BayIED", "S1")
if err != nil { return err }
runtime, err := simulator.Start(ctx, model, simulator.Options{
    Listen: "127.0.0.1:8102",
    GOOSE: simulator.GOOSEMemory,
})
if err != nil { return err }
defer runtime.Close()

client, err := ied.Dial(ctx, runtime.Address(), ied.Config{})
if err != nil { return err }
defer client.Close()
subscription, err := client.SubscribeURCB(ctx,
    mms.DomainName("BayIEDCTRL", "LLN0$RP$events01"),
    ied.ReportOptions{GI: true})
if err != nil { return err }
defer subscription.Close()
```

`model.Reports()` enumerates the actual indexed instances, including `events01`
and `history01`. ReportControl, RptEnabled.max, FCDA order, confRev, TrgOps,
OptFields, bufTime and intgPd are taken from the SCD. Commit value, quality and
source time together through `Signal.Sample` and `model.Update`. DA dchg/qchg/
dupd declarations determine event inclusion. GI and integrity include all
members. A second change of a pending member flushes its earlier event before
replacing it. Negotiated MMS PDU limits determine segmentation. Subscriptions
deliver a complete validated report after reassembly.

URCB ownership belongs to one association. A BRCB retains a bounded in-memory
history across association loss and transport restart while the same IED
model is retained. The default capacity is 256 records per instance. Call
`model.SetReportBufferCapacity` before acquisition/serving to choose 1..4096.
The model and buffer are owned by the application process.

## Resume buffered history

```go
subscription, err := client.SubscribeBRCB(ctx,
    mms.DomainName("BayIEDCTRL", "LLN0$BR$history01"),
    ied.ReportOptions{ResumeEntryID: lastHandledEntryID})
if err != nil { return err }
defer subscription.Close()
for event := range subscription.Events() {
    if event.Err != nil { return event.Err }
    report := event.Report
    if report.Reset { handleHistoryGap() }
    if err := storeEvents(report.Entries); err != nil { return err }
    // Persist the cursor after your application handles the complete report.
    lastHandledEntryID = append([]byte(nil), report.EntryID...)
}
return subscription.Err()
```

`lastHandledEntryID`, `storeEvents` and `handleHistoryGap` belong to your
application. A missing cursor leaves the server's current transmission cursor
in effect. An all-zero eight-byte cursor requests retained history from its
beginning. An expired/unknown cursor is rejected. A received BufOvfl marks
`BufferOverflow` and `Reset`. `PurgeBuffer: true` explicitly discards retained
history and is mutually exclusive with ResumeEntryID. `Interrogate(ctx)`
requests GI on an existing subscription.

`Report.SequenceNumber` retains the 8-bit URCB or 16-bit BRCB sequence.

Use a dataset with a complete ST FCD when each event needs value, q and source
t together. The sample history01 uses dsEvents for this purpose. `report.Leaves()`
expands that typed structure into named scalar entries from the same report.
Subscription setup obtains the selected members' online types and validates
every received value before delivery. Configure EntryID in the BRCB OptFlds
when the application needs a durable resume cursor.

`Sequence` remains available for URCB callers. The optional fields carried on
the wire are validated when present. Dataset identity and revision otherwise
come from the declarations read during subscription setup. `TimeOfEntry`
retains the MMS report time. A data object's `t` retains its source timestamp.
`ReceivedAt` records local receipt and supports monotonic age checks.

## Control and configure equipment

`model.OnOperate` retains the ordinary direct binary control interface.
`model.OnControl` binds a six-field SPC/DPC Oper for ctlModel 1..4 and handles
association ownership, SBO/SBOw, expiry, Cancel and enhanced termination.
SBO/SBOw and Cancel must be declared in the SCL. The callback implements the
actuator, requested checks and independently acquired feedback.

For an explicit selection phase use `control.Select(ctx, value, options)`,
followed by `selection.Operate(ctx)` or `selection.Cancel(ctx)`. The selection
is single use. Automatic select/operate remains available through
`client.Operate`. Read ST feedback separately to establish physical position.

For ordinary SP settings register `model.OnSetpoint(name, callback)`. Validate
and apply the requested configuration in the callback. A rejected callback
preserves the prior SP value. A successful callback commits the modeled value.

For setting groups use `client.SettingGroups(ctx, domain)` and
`client.EditSettings(ctx, domain, group, values, activate)`. The values map uses
SE leaf names. The helper prevalidates types, reserves EditSG, writes the edit
copy, confirms it and optionally activates it. Its result records Committed and
Activated independently. SG remains the active view while SE edits another
group. Edit reservations expire after 60 seconds or association loss. The
[settings example](../../examples/ied/settings/main.go) shows these operations.

## Connect multiple IEDs in software

Pass the same `simulator.MemoryBus` to separate runtimes. Each bus endpoint
has its own reader and bounded queue. GOOSE and SV share complete Ethernet
frames, including VLAN headers. A full endpoint rejects the broadcast with an
explicit error. Cancel and join receivers before unregistering or closing links.

`System.SVInputs` enumerates SMV ExtRefs. `System.SVInput(receiver, ap, intAddr)`
resolves a scalar input to its source profile, FCDA field and related quality.
`System.GOOSEInputs` and `IED.Input` provide the corresponding GOOSE path.
`simulator.Options.SV` starts software sources on a shared bus.
`simulator.RunSampledValues` also accepts a caller-owned Ethernet FrameIO.
SampleSync defaults to zero. The example explicitly accepts this software
clock and uses fixture-specific scaling. Ordinary Go timers provide the demo
sampling cadence. Physical sampling, synchronization and performance require
the equipment driver's clock and commissioning measurements.

The examples' SCL structure is validated against the 2007B4 XSD. IEC model and
service references are [IEC 61850-6](https://webstore.iec.ch/en/publication/103863),
[IEC 61850-7-2](https://webstore.iec.ch/en/publication/66525),
[IEC 61850-7-3](https://webstore.iec.ch/en/publication/66526) and
[IEC 61850-7-4](https://webstore.iec.ch/en/publication/66551).


## Run the complete station

Run `go run ./cmd/iedlab serve`, choose **Station sample**, then **Start station**.
Each server AP receives its own loopback MMS listener. The station panel shows
decoded GOOSE/SMV ExtRef inputs and counters. Edit MUA acquisition values to
change subsequent SV samples. Operate BayIED through MMS to observe the HMI's
GOOSE position input. Stop the station before starting a member individually.

The headless equivalent is:

```sh
go run ./cmd/iedlab simulate -station -file examples/scd/station/station.scd
```

Applications use the same public runtime:

```go
station, err := simulator.StartStation(ctx, system, simulator.StationOptions{
    Observe: func(input simulator.InputReading) {
        fmt.Println(input.Receiver, input.Address, input.Value, input.Error)
    },
})
if err != nil { return err }
defer station.Close()
for _, device := range station.Devices() {
    fmt.Println(device.IED, device.AP, device.Runtime.Address())
}
```

The default `ModelSVSource` reads the MU dataset in one acquisition snapshot,
converting MMS Quality to SV's uint32 flag representation. It transmits current
model values; it does not infer scaling, a waveform or a synchronized clock.
Override `StationOptions.Sources[profile.Reference]` with a sampling callback.
IntAddr names an application input. Observe it and explicitly update your
receiver's model to implement calculations or equipment logic. The waveform
and power calculation in the station example remain application code.

## Configuration changes, arrays and control completion

SP writes and active SG changes now publish through the same coherent snapshot
boundary as acquisition. An inactive group edit does not publish a change to
the active view. A read-only child also protects writes to its enclosing value.

Arrays support 1..4096 elements within the global expansion budget. DOI/SDI/DAI
initial values use zero-based `ix`. FCDA `component(index)` paths retain actual
nested MMS AlternateAccess. Update the enclosing immutable array through
`IED.Update`; report triggers isolate selected elements and q/value changes.
`mms.Selection.Path` expresses ordered component/index selections for clients.

An unbound ReportControl is inspectable but cannot be enabled. While disabled,
DatSet may select an existing static dataset in the same logical device;
ConfRev increases. Buffered history must be explicitly purged before rebinding.
Dynamically created MMS lists are not RCB datasets in this profile.

For enhanced binary controls, OnControl runs after successful MMS Operate
acceptance. A callback failure produces negative CommandTermination. The client
can distinguish Acknowledged from Terminated. The execution context ends when
the association closes. Normal-security callbacks remain synchronous.

## Online discovery and current snapshots

Available in v1.4.0. See the [API entry point](README.md) for imports, ownership and result contracts; the [GoDoc examples](../../ied/example_test.go) execute discovery and current reads against a local MMS server.

Connect without SCL and discover actual datasets, control blocks and types.

```go
ctx := context.Background()
client, err := ied.Dial(ctx, "127.0.0.1:8102", ied.Config{})
if err != nil { return err }
defer client.Close()
directory, err := client.Discover(ctx, ied.DiscoveryOptions{MaxObjects: 4096})
if err != nil { return err }
for _, block := range directory.Reports {
    fmt.Println(block.Name, block.DataSet, block.ConfRev, block.Error)
}
```

Check Diagnostics and Truncated before treating discovery as complete. Network,
primary equipment and ExtRef information comes from SCL. A discovery context
bounds the operation. Unsupported objects do not hide other usable objects.

SubscribeBRCB continues to deliver complete retained events. Commit EntryID
only after the application has durably handled that report. A GI reason can
belong to an earlier session. `subscription.ReadSnapshot(ctx)` provides an
independent current dataset Read; its result has no EntryID and must not advance
the history cursor. Use `client.ReadDataSet(ctx, name)` without a subscription.
Both results provide `Leaves()` to expand typed FCD values with their q and t.
A response contains one MMS transaction. Coherence between separate FCDA values
depends on the remote store. Prefer FCD members for related value/q/t.

The built-in Model and SCL IED now serve batch reads from one acquisition image.
A custom store may implement mms.BatchReader. Existing stores keep their Read
behavior; decorators explicitly forward batching when supported.

## Engineering validation and application bindings

`system.Project()` returns SubNetworks, AP/ServerAt ownership, primary equipment
and supported semantic diagnostics. `system.Inputs(ied, ap)` lists every service
with receiving LD/LN and declaration position. InputReference.Key remains stable
without intAddr. ResolveGOOSEInput and ResolveSVInput accept that scoped reference.
The earlier intAddr lookup methods remain available. CompareCatalog compares
point types, units, quality/time bindings, ordered FCDA selections, report
revisions and input sources. ReportSettings preserves Fix/Conf/Dyn declarations;
only Dyn permits online RCB field changes. These supported semantic checks do
not replace an edition-matching external XSD or full NSD validation.

StartStation owns one bus per SubNetwork. ServerAt exposes another physical
port of the same server without bridging networks. StrictInputs rejects
unresolved/disconnected inputs; normal inspection retains them as unbound.

```go
station, err := simulator.StartStation(ctx, system, simulator.StationOptions{
    Models: map[string]*scl.IED{"BayIED/S1": bay},
    StrictInputs: true,
    InputTimeout: time.Second,
    OnSample: func(ctx context.Context, group simulator.SampleEvent) error {
        // All fields belong to one ASDU, including unchanged samples.
        for _, input := range group.Inputs {
            fmt.Println(input.Address, input.Value, input.Valid, input.Quality)
        }
        return nil
    },
})
if err != nil { return err }
defer station.Close()
```

Replace the callback body with acquisition calculations and commit outputs
through bay.Update. The runnable station example supplies the full three-phase
power calculation, sample source and independent control/GOOSE feedback. OnGOOSE
delivers complete accepted status events. Observe emits changes for interfaces.
Callbacks return promptly and perform shutdown from the owner.

InputReading retains Value and ReceivedAt separately from Valid, State,
ValidUntil and LastGoodAt. Station conservatively marks nonzero source quality
unusable and retains all quality bits. SampleSync remains separately visible.
GOOSE expires by TAL and SV by InputTimeout. NominalFrequency supports 50/60 Hz.
The software scheduler provides no synchronized physical sampling clock.
