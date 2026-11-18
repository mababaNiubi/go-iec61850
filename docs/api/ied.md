# Model-based clients and embedded simulators

English | [简体中文](ied.zh-CN.md)

Use `ied` for IEC 61850 controls, URCB/BRCB subscriptions, online discovery, current dataset reads and setting groups. `mms` provides raw MMS services. `simulator` runs an SCL-backed MMS/GOOSE/SV device or software station in your process without depending on IEDLab. Start with the [API task index and calling contracts](README.md); [engineering scenarios](engineering.md) cover model-driven reporting, history and station bindings.

## Connect, read and control

Inside a function returning `error`, import `context`, `time`, and the public `data` and `ied` packages:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

client, err := ied.Dial(ctx, "127.0.0.1:8102", ied.Config{
    Origin: ied.Origin{Category: 2, ID: []byte("my-station")},
})
if err != nil { return err }
defer client.Close()

name, err := ied.ObjectName("BayIEDCTRL", "MX", "MMXU1.TotW.mag.f")
if err != nil { return err }
value, err := client.Read(ctx, name)
if err != nil { return err }
watts, err := value.Float()
if err != nil { return err }
_ = watts // pass to your application

result, err := client.Operate(ctx, "BayIEDCTRL/XCBR1.Pos", data.Boolean(true),
    ied.ControlOptions{InterlockCheck: true})
if err != nil { return err }
_ = result.Acknowledged
```

`Config.MMS` accepts the existing `mms.Config`, including ISO selectors and request timeouts. When loading SCL, use the selected model's `ClientConfig()`. The Dial context bounds setup; each subsequent request has its own context. Close releases the association and its subscriptions.

`Operate` reads `CF.ctlModel` and the online `CO.Oper` declaration. The session serializes select/operate pairs and dispatches enhanced-security terminations on the same association:

| Model | Flow |
|---|---|
| 1 / `DirectNormal` | Write Oper |
| 2 / `SBONormal` | Read SBO selection, then write Oper |
| 3 / `DirectEnhanced` | Write Oper and await matching CommandTermination |
| 4 / `SBOEnhanced` | Write SBOw selection, then Oper and await termination |

The builder follows declared component order and optional ctlNum/operTm fields. `ControlOptions.OperTime` fills operTm, so a peer that declares it schedules execution at that time (enhanced security only); zero means immediate. Test, interlock and synchrocheck requests are explicit options, with execution decided by the IED.

For a nonbinary control, first call `client.Control(ctx, "LD/LN.object")`, then `control.ValueType()`. Construct a matching `data.Integer`, `data.Unsigned`, `data.Float32` or other declared value and call `control.Operate`. Obtain a new control object after device reconfiguration.

`ControlResult.Selected`, `Acknowledged` and `Terminated` describe selection, MMS acceptance and enhanced command termination. Read or subscribe to ST feedback separately to establish the physical position. Errors preserve their causes for `errors.Is`:

| Error | Application action |
|---|---|
| `ied.ErrNotSubmitted` | Oper was not submitted; inspect the setup/selection error |
| `ied.ErrRejected` | The device or local type validation rejected the command |
| `ied.ErrUnconfirmed` | Transmission/termination is uncertain; inspect feedback before deciding further action |

The SDK never automatically replays an uncertain command. [Runnable control and report example](../../examples/ied/control-report/README.md)

## Subscribe to an engineered URCB

Use a control-block instance allocated to this client. The subscription uses its engineered dataset, trigger options and OptFlds. It validates optional dataset/revision fields when present and delivers complete reassembled reports. `SubscribeBRCB` supports buffered replay with an explicit EntryID cursor. See the [engineering API](engineering.md) for modeled reporting, resume and settings.

```go
block, err := ied.ObjectName("LD", "RP", "LLN0.Events01")
if err != nil { return err }
sub, err := client.SubscribeURCB(ctx, block, ied.ReportOptions{Buffer: 64})
if err != nil { return err }
defer sub.Close()

for {
    select {
    case <-ctx.Done():
        return ctx.Err()
    case event, ok := <-sub.Events():
        if !ok { return sub.Err() }
        if event.Err != nil {
            // Invalidate your cached members; poll or rebind as appropriate.
            continue
        }
        report := event.Report
        if report.Reset {
            // Sequence discontinuity: discard members cached from older reports.
        }
        for _, entry := range report.Entries {
            _ = entry.Variable.Name
            _ = entry.Value
            _ = entry.Reason
        }
    }
}
```

Setup reserves the RCB where supported and registers before enabling RptEna, so an early report is retained. An enabled RCB or duplicate report ID on the association is rejected. The setup context does not determine subscription lifetime: use `Stop(ctx)`, `Close()`, or close the client. Ambiguous enable/release errors close the association to release ownership.

The entire report is checked before any entry is returned. A value and its q should come from the same report if the application requires a coherent sample. Initial values may be obtained by confirmed Read; subscribing does not force GI or guarantee an immediate report. Cache age and fallback polling are application policy.

A slow consumer closes its queue with `ErrReportOverflow`; it cannot block a command termination or another subscription. `client.MMS()` exposes advanced confirmed services. **Do not consume its raw `Reports()` channel**: `ied.Client` owns that channel.

## Keep quality and scalar policies explicit

`ied.DecodeQuality` converts a 13-bit MMS/GOOSE Quality to `ied.Quality`, using the same low-bit flag numbering as an SV Quality word. `q.MMSValue()` converts it back. `GoodValidity()` only checks validity; use `Has(ied.OldData | ied.Test | ...)` according to your application's rules. `Valid()` detects reserved high bits in an SV word.

`data.Value` retains signed/unsigned integers, Float32 width and raw UTC quality bytes. Converting time to Unix milliseconds, rounding floats, scaling engineering units, or deciding that all flagged values are unusable belongs to the caller. `data.ResolveComponent`, `ValueAt` and `WithComponent` support ordered structures and zero-based arrays while preserving untouched siblings.

## Embed a simulator

```go
system, err := scl.ParseFile("device.scd")
if err != nil { return err }
model, err := system.Device("BayIED", "S1")
if err != nil { return err }

// Bind model.OnControl handlers and initialize acquisition values here.
runtime, err := simulator.Start(ctx, model, simulator.Options{
    Listen: "127.0.0.1:8102",
    GOOSE: simulator.GOOSEMemory,
})
if err != nil { return err }
defer runtime.Close()
return runtime.Wait(ctx)
```

Start owns TCP connections, its listener, frame links and protocol workers. `Address()` reports the actual address when using port zero. Parent cancellation or `Stop(ctx)` stops transport; the model remains available to update and restart. Local acquisitions use `model.Update`; hardware execution uses `model.OnControl`. The modeled SPC/DPC server accepts the six-field Oper and the seven-field form with operTm, supporting selection, enhanced termination and time-activated execution according to ctlModel; `OnOperate` retains the direct-normal interface. Other control types exposed by a remote IED can be used by the client according to their online declarations. See [control completion and model limits](engineering.md#configuration-changes-arrays-and-control-completion).

Memory mode encodes, publishes and receives GOOSE Ethernet frames without a NIC. Ethernet mode requires `Interface` and uses its actual source MAC. `Observe` is an optional concurrent callback for events and errors. Return promptly and perform shutdown outside it. An optional Store decorator can trace real MMS accesses. `MemoryBus` connects independent runtimes, and `Options.SV` starts software sampled-value sources on that bus. See the [station example](../../examples/scd/station/README.md).

## Bind, verify and reconfigure an acquisition

`ReadDataSet` stays a one-shot convenience. For repeated acquisition, `BindDataSet` reads the ordered members and their selected types once and returns a handle whose `Read` reuses that metadata instead of requesting one type per member on every poll. `Info()` reports a local `GenerationID` — a counter, not a remote ConfRev.

```go
binding, err := client.BindDataSet(ctx, mms.DomainName("BayIEDCTRL", "LLN0$dsPos"), ied.BindOptions{VerifyEachRead: true})
if err != nil { return err }
defer binding.Close()
snapshot, err := binding.Read(ctx)          // ErrConfigurationChanged when the peer reconfigures
info, err := binding.Refresh(ctx)           // publish the next generation
```

Double reads detect observed changes; they do not create an atomic remote transaction and cannot exclude an unseen change-and-revert. With `VerifyEachRead` a change returns `ErrConfigurationChanged` (details via `*ied.GenerationChangeError`) and leaves the binding stale until `Refresh`. `ReadDataSetVerified` applies the same CFG-01 double read to a one-shot snapshot and marks it `Verified`; `ConfRev` stays zero-meaning-unknown unless an RCB was verified. A report decoder that sees a changed dataset, revision or inclusion bitmap terminates its subscription with the same typed error, so a late report cannot update the previous generation.

```go
err = client.CreateDataSet(ctx, mms.DomainName("BayIEDCTRL", "LLN0$dsDyn"), []mms.VariableAccess{position})
configured, err := client.ConfigureRCB(ctx, block, ied.RCBConfiguration{DataSet: mms.DomainName("BayIEDCTRL", "LLN0$dsDyn")})
```

`CreateDataSet` validates every member locally before defining a domain list. `ConfigureRCB` takes ownership through `Resv`, writes `DatSet` (the peer bumps `ConfRev` itself), re-reads the declaration and resolves the bound members; it never enables the block, and an enabled block is refused. A peer that denies an online field reports its error unchanged, so Fix/Conf/Dyn restrictions stay visible. `DeleteDataSet` returns the service's `Matched`/`Deleted` result; engineered static datasets are reported as matched-but-not-deleted.

`Discover` charges every request, decoded name and object against one budget (`MaxObjects`, `MaxRequests`, `MaxBytes`); exhaustion stops before further work, sets `Truncated` with a `TruncationReason`, and preserves completed results for a re-run with a larger budget. `BuildModelTree` groups a result into device → logical node → FC → point without inventing semantics the peer did not return. `NewSelectionDocument` + `VerifySelection` revalidate a saved selection point by point (`ok`, `missing`, `type-changed`) without modifying remote state; unknown document versions fail explicitly. `Capabilities()` returns the reviewed role/service profile — unsupported services are not advertised on the wire.

## Manage GOOSE and SV control blocks online

```go
gocb := mms.DomainName("BayIEDCTRL", "LLN0$GO$gcbPos")
gcb, err := client.ReadGoCB(ctx, gocb)
err = client.SetGoCBEnabled(ctx, gocb, false)   // GoEna gates publication

svcbName := mms.DomainName("MUALD0", "LLN0$MS$MSVCB01")
svcb, err := client.ReadSVCB(ctx, svcbName)
err = client.SetSVCBEnabled(ctx, svcbName, true) // SvEna
```

Only fields with settled online behavior are written: `GoEna` and `SvEna`. Dataset rebinding and destination-address changes alter the engineered stream layout and are refused instead of silently ignored. The modeled server exposes GoCB and SVCB attributes and gates GOOSE/SV publication on `GoEna`/`SvEna`. BRCB reservation reports `ResvTms` (-1 indefinite, 0 free, positive seconds) and `Owner` (client address bytes) with the reference implementation's semantics; these last two are implementation-derived, and the SVCB `OptFlds` bit assignment is not verified against normative text. Time-activated control uses `ControlOptions.OperTime` against a peer whose Oper declares `operTm` (enhanced security only); zero means immediate. `SetAccessHandler` applies a remote read/write policy without touching local acquisition.

## Secure the transport with TLS

`mms.DialTLS` and `(*mms.Server).ServeTLS` place TLS between TCP and the ISO session; no ISO, ACSE or MMS byte changes. Scope is transport confidentiality, integrity and optional mutual certificate authentication — **not** IEC 62351-4 end-to-end security, ACSE authentication or role-based authorization.

```go
policy := mms.TLSPolicy{Certificates: []tls.Certificate{serverCert}, ClientCAs: clients, ClientAuth: tls.RequireAndVerifyClientCert}
go server.ServeTLS(listener, policy)

client, err := mms.DialTLS(ctx, address, mms.Config{}, mms.TLSPolicy{RootCAs: roots, ServerName: "server.ied.test"})
```

The zero policy is unusable by design: a server needs a certificate, and client-certificate verification needs `ClientCAs`. `MinVersion` defaults to TLS 1.2 and lower values are rejected. `InsecureSkipVerify` is refused unless `AllowInsecure` is also set; even then the reported `SecurityEvent.Verified` stays false, so an audit never mistakes an unverified peer for a verified one. `OnEvent` receives redacted observations (no certificate or key material). Plaintext and secured endpoints do not interoperate in either direction — there is no silent downgrade. TLS 1.3 cipher suites are fixed by the Go runtime; `CipherSuites` only restricts TLS 1.2. `CRLs` adds revocation lists to peer verification, `KnownCertificates` pins the accepted peer leaf (any other valid chain is refused), and `client.PeerCertificate()` exposes the verified peer leaf for local policy decisions.

## Import an SV model and share one reader

`system.SVStream("MUA/AP1/LD0/MSVCB01")` returns an `sv.Profile`; `SVStreams(ied, ap)` enumerates that AP's streams without constructing an MMS runtime. `profile.Layout.Field(member, path)` selects a scalar by FCDA item or index. `QualityFor(field)` finds a modeled q in the same data object. `Field.Decode`/`Encode` use checked offsets and original SCL types, including four-octet SV Enum/Dbpos/Quality/Check.

Create a subscriber with `profile.SubscriptionConfig(sv.SubscriptionOptions{})`, then add it to a single `sv.Receiver` with `profile.ValidateFrame` and a callback. Add each additional stream to that receiver and call `Run(ctx, link)` once. [Runnable SCL/SV example](../../examples/sv/scl/README.md)

`goose.Receiver` provides the same shared-reader pattern for GOOSE. Callbacks run synchronously on the receive goroutine: keep them short or hand work to a bounded application queue. Receiver callbacks report packet results; use `Snapshot()` or `CheckTimeout()` from the application's polling/timer loop to detect expiry when traffic stops. `Subscriber.Events()` belongs to the standalone `Subscriber.Run` API and is not also populated by Receiver. Cancel and join Run before unregistering/closing its link. The returned unregister function waits for dispatch and must be called outside callbacks.

For GOOSE dataset access, use `stream.Layout()` and `stream.Values(ctx)` on an SCL stream. `layout.Field(member, path)` compiles an allData field; `Read` and `Set` preserve structure siblings. `CheckCompatible` compares member order, logical-device relationships and declared types before reusing a point mapping for another stream. `LeafCount` supports complete publication initialization policies.

## Adapting a gateway

Keep channel management, retry policy, point templates, units/scales, database models, application clocks/snapshots, scalar adapters and write-status mapping in the gateway. Import public `ied`, `scl`, `data`, `goose`, `sv` and `simulator` packages for protocol behavior. The SDK does not import gateway code or depend on a scalar-variant library. IEDLab uses these same `ied` and `simulator` APIs.
