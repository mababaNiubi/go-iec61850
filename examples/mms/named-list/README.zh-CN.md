# 按MMS命名变量表读写

[English](README.md) | 简体中文

从仓库根目录运行：

```bash
go run ./examples/mms/named-list
```

AA列表只属于当前关联；代码顺序是Define→Write→Read→Delete→Conclude。逐项检查读写错误。见[API](../../../docs/api/mms.zh-CN.md#按命名变量表成组读写)。

[带步骤注释的代码](main.go)。关键输出：

```text
watch[0] = 100
watch[1] = false
deleted lists: 1
```
