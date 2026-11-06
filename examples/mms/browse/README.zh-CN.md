# 发现设备目录和变量类型

[English](README.md) | 简体中文

从仓库根目录运行：

```bash
go run ./examples/mms/browse
```

Names自动处理分页；GetVariableAccessAttributes返回设备声明约束。见[API](../../../docs/api/mms.zh-CN.md#发现-domain变量名和类型)。

[带步骤注释的代码](main.go)。关键输出：

```text
domains: [sample]
sample variables: [array bool float int readonly struct struct$count struct$flag text]
sample/int: type=integer width=32
```
