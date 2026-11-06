# 自定义SV采样布局

[English](README.md) | 简体中文

从仓库根目录运行：

```bash
go run ./examples/sv/data-layout
```

打开main.go先看偏移表，再看逐个Set和读取。数字使用网络字节序，不依赖Go结构体对齐。见[API](../../../docs/api/sv.zh-CN.md#自己定义采样值布局)。

[带步骤注释的代码](main.go)。关键输出：

```text
value=-123 quality=0x400 float=1.25 negativeZero=true utc=6553f1008000000a
```
