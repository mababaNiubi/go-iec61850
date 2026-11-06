# Read and write an MMS integer

English | [简体中文](README.zh-CN.md)

Run from the repository root:

```bash
go run ./examples/mms/read-write
```

The example starts a temporary loopback server by default, connects with `mms.Dial`, reads `sample/int`, writes 99, and reads it back. It checks the integer accessor error, concludes the association, and closes resources.

To use an existing sample peer, add `-address 127.0.0.1:8102`. See [connection and naming parameters](../../../docs/api/mms.md#connect-and-read-or-write-an-integer).

[Annotated source](main.go). Expected key output:

```text
sample/int: 42 -> 99
```
