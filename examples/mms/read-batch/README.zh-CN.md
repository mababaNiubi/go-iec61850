# 批量读取与部分失败

[English](README.md) | 简体中文

从仓库根目录运行：

```bash
go run ./examples/mms/read-batch
```

重点看ReadMultiple返回的整体err与每个ReadResult.Error，再看Element的0起始下标及Member。见[API](../../../docs/api/mms.zh-CN.md#一次读取多个变量)。

[带步骤注释的代码](main.go)。关键输出：

```text
sample/int = 42 (type=integer)
sample/bool = true (type=boolean)
sample/missing: mms: object nonexistent
array[1]=2, struct.count=7
```
