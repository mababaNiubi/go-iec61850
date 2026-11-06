# SV发布和接收采样

[English](README.md) | 简体中文

从仓库根目录运行：

```bash
go run ./examples/sv/publish-subscribe
```

8字节布局为INT32+Quality。UpdateData的0是ASDU索引；采样计数由Publisher推进，接收Process返回多个事件。见[API](../../../docs/api/sv.zh-CN.md)。

[带步骤注释的代码](main.go)。关键输出：

```text
sample: smpCnt=0 value=100 quality=0x0
sample: smpCnt=1 value=101 quality=0x0
```
