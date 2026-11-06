# Discover names and declared types

English | [简体中文](README.zh-CN.md)

Run from the repository root:

```bash
go run ./examples/mms/browse
```

`Names` discovers domains and variables, collecting pages automatically. `GetVariableAccessAttributes` returns the device's declared type constraints; this example prints the integer width.

A temporary loopback peer is started by default; `-address host:port` selects an existing sample peer. See [browsing names and types](../../../docs/api/mms.md#discover-domains-variable-names-and-types).

[Annotated source](main.go). Expected key output:

```text
domains: [sample]
sample variables: [array bool float int readonly struct struct$count struct$flag text]
sample/int: type=integer width=32
```
