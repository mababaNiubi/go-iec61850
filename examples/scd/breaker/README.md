# A breaker IED and a station monitor

English | [简体中文](README.zh-CN.md)

Run this from the repository root for one complete closed-loop demonstration. It uses a temporary MMS loopback listener, real MMS requests and encoded GOOSE frames on an in-memory Ethernet link.

```sh
go run ./examples/scd/breaker
```

```text
GOOSE BayIEDCTRL/XCBR1$ST$Pos$stVal stNum=1 position=open
MMS active power=0 W position=open
MMS close blocked by interlock
MMS active power=1250 W position=closed
GOOSE BayIEDCTRL/XCBR1$ST$Pos$stVal stNum=2 position=closed
MMS active power=0 W position=open
GOOSE BayIEDCTRL/XCBR1$ST$Pos$stVal stNum=3 position=open
```

| Start here | What to change |
|---|---|
| [bay.scd](bay.scd) | Edit IED/AP/LD, LN templates and points, dataset FCDA order, GSEControl, Communication/GSE and receiver ExtRef |
| [process.go](process.go) | Bind your signal names and implement `processIO.Read` and `Operate` using your driver or interlock logic |
| [monitor.go](monitor.go) | Poll MMS, send a complete Oper, consume the ExtRef's value and mark stale GOOSE data |
| [main.go](main.go) | Select the IED/AP, create the server and links, run the loops and coordinate shutdown |

## Run the two applications separately

In terminal 1, start the simulated IED. In terminal 2, start the monitor. Both use the SCD's loopback endpoint on port 8102 and can run on the same machine. The monitor polls until interrupted.

```sh
go run ./examples/scd/breaker -role ied
```

```sh
go run ./examples/scd/breaker -role monitor
```

To send a single command to this simulated IED, start the monitor with `-operate close` or `-operate open`. Without that flag it only monitors. The standalone IED starts with its close permissive enabled. For GOOSE between separate processes, add `-interface <interface>` to **both** commands and use a Layer 2 path that delivers the multicast and VLAN in the SCD. The two roles can use different interfaces on different hosts. Use `-address host:port` for an explicit MMS listen/dial endpoint. On Windows, `-promiscuous` is available for reception. See [platform support](../../../docs/platforms.md).

## Use your model

Start by inspecting the available IEDs, then list the paths of interest:

```sh
go run ./cmd/scdinspect -file examples/scd/breaker/bay.scd
go run ./cmd/scdinspect -file examples/scd/breaker/bay.scd -ied BayIED -ap S1 -match XCBR1.Pos
```

Pass `-file your.scd -ied <ied> -ap <ap> -ld <instance>` to use a model with the same application signals. Other models should adapt `bindPoints` to their own LN/DO/DA semantics, and select their own block name and subscriber ExtRef instead of the example's `gcbPos` and `StationHMI/S1/breakerPosition`. The public `scl` package resolves the identifiers and wire format. The [SCD API guide](../../../docs/api/scl.md) includes the path table, integration calls and supported profile.
