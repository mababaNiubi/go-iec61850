# Control an IED and receive URCB reports

English | [简体中文](README.zh-CN.md)

Run from the repository root:

```sh
go run ./examples/ied/control-report
```

The default example loads the shared BayIED SCL, binds a virtual actuator, starts the public `simulator` runtime, and sends an actual MMS control using `ied.Client`. The output distinguishes protocol acknowledgement from termination, then separately reads position feedback.

Connect to an engineered IED and subscribe to its allocated URCB:

```sh
go run ./examples/ied/control-report -address 127.0.0.1:8102 -object simpleIOGenericIO/GGIO1.SPCSO1 -urcb LLN0.ControlEventsRCB01 -on=true
```

This command sends a real control to the supplied address. The optional URCB must exist and include dataset and ConfRev in its reports. The local sample has ordinary direct control and no URCB server. To integrate your application, retain the public `ied` calls and bind your own SCL/device driver; no workbench package is imported. [API and result semantics](../../../docs/api/ied.md)
