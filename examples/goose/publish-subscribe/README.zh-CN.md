# GOOSE发布和接收状态

[English](README.md) | 简体中文

从仓库根目录运行：

```bash
go run ./examples/goose/publish-subscribe
```

frame明确写出两端标识，Update改变业务值，Publish单次发送，receive先DecodeFrame再Process。真实持续发布改用Run。见[API](../../../docs/api/goose.zh-CN.md)。

[带步骤注释的代码](main.go)。关键输出：

```text
state-changed: st=1 sq=0 active=false
state-changed: st=2 sq=0 active=true
```
