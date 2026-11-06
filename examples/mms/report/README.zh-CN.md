# 发送和接收MMS报告

[English](README.md) | 简体中文

从仓库根目录运行：

```bash
go run ./examples/mms/report
```

main.go前半部分表示服务端，后半部分表示客户端。Association.SendReport发送一份快照，Reports接收它；不会自动建立RCB。见[API](../../../docs/api/mms.zh-CN.md#接收和发送-informationreport)。

[带步骤注释的代码](main.go)。关键输出：

```text
report sample/int = 123
```
