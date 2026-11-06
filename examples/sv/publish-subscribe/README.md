# Publish and receive SV samples

English | [简体中文](README.zh-CN.md)

Run from the repository root:

```bash
go run ./examples/sv/publish-subscribe
```

The eight-byte payload is INT32 followed by Quality. The zero in `UpdateData(0, raw)` selects the first ASDU. The publisher advances the sample counter, and subscriber `Process` returns one event per matching ASDU.

An in-memory link is used. The example explicitly accepts its unsynchronized software samples; no NIC or sampling hardware is required. See the [SV API](../../../docs/api/sv.md).

[Annotated source](main.go). Expected key output:

```text
sample: smpCnt=0 value=100 quality=0x0
sample: smpCnt=1 value=101 quality=0x0
```
