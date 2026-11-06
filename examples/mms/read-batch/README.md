# Batch reads and partial failures

English | [简体中文](README.zh-CN.md)

Run from the repository root:

```bash
go run ./examples/mms/read-batch
```

Follow the request-level `err` from `ReadMultiple` and the individual `ReadResult.Error` values. A missing item does not hide the successful results. The example then uses zero-based `Element` and named `Member` access to read parts of an array and structure.

A temporary loopback peer is started by default; `-address host:port` selects an existing sample peer. See [batch access](../../../docs/api/mms.md#read-several-variables).

[Annotated source](main.go). Expected key output:

```text
sample/int = 42 (type=integer)
sample/bool = true (type=boolean)
sample/missing: mms: object nonexistent
array[1]=2, struct.count=7
```
