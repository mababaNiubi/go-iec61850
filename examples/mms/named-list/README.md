# Read and write an MMS named list

English | [简体中文](README.zh-CN.md)

Run from the repository root:

```bash
go run ./examples/mms/named-list
```

An AA list belongs to the current association. Follow the sequence: define, write, read, delete, and conclude. Check individual read/write failures before using results. The list references existing variables; it does not configure an SCL dataset or RCB.

A temporary loopback peer is started by default; `-address host:port` selects an existing sample peer. See [named variable lists](../../../docs/api/mms.md#read-and-write-a-named-variable-list).

[Annotated source](main.go). Expected key output:

```text
watch[0] = 100
watch[1] = false
deleted lists: 1
```
