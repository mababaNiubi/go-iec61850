# Send and receive an MMS report

English | [简体中文](README.zh-CN.md)

Run from the repository root:

```bash
go run ./examples/mms/report
```

The first part of `main.go` creates the server; the second creates the client. `Association.SendReport` sends a value snapshot and the client's `Reports()` channel receives it. The example includes both local endpoints and shuts down their background work.

This is raw InformationReport handling; no report control block is created automatically. See [report APIs](../../../docs/api/mms.md#receive-and-send-informationreport).

[Annotated source](main.go). Expected key output:

```text
report sample/int = 123
```
