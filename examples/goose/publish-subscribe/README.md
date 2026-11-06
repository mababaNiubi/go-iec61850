# Publish and receive GOOSE states

English | [简体中文](README.zh-CN.md)

Run from the repository root:

```bash
go run ./examples/goose/publish-subscribe
```

The frame declares the identifiers used by both endpoints. `Update` changes the application value, `Publish` sends once, and the receive path calls `DecodeFrame` followed by `Process`. The publisher advances the state counter from false to true.

An in-memory link is used, so no NIC is opened. Use `Run` for continuous publication and automatic retransmission. See the [GOOSE API](../../../docs/api/goose.md).

[Annotated source](main.go). Expected key output:

```text
state-changed: st=1 sq=0 active=false
state-changed: st=2 sq=0 active=true
```
