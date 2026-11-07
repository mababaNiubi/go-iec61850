# 按业务场景看示例

[English](README.md) | 简体中文

每个目录只讲一个场景，直接打开main.go。代码中的中文注释说明调用顺序、字段含义、错误和资源关闭；API参数详解在 [API手册](../docs/api/README.zh-CN.md)。

| 我要学习 | 代码 | 运行 |
|---|---|---|
| 控制 IED、检查确认结果、订阅 URCB | [设备客户端](ied/control-report/README.zh-CN.md) | `go run ./examples/ied/control-report` |
| 按 SCL 解码 SV，无需写死偏移 | [SCL/SV](sv/scl/README.zh-CN.md) | `go run ./examples/sv/scl` |
| 根据 SCD 接入设备采集、控制和监控订阅 | [断路器模型和两端代码](scd/breaker/README.zh-CN.md) | `go run ./examples/scd/breaker` |
| 连接，读一个整数，写入，再读回 | [MMS读写](mms/read-write/main.go) | `go run ./examples/mms/read-write` |
| 批量读取、逐项失败、数组/结构选择 | [MMS批量](mms/read-batch/main.go) | `go run ./examples/mms/read-batch` |
| 查询Domain、变量名和声明类型 | [MMS目录](mms/browse/main.go) | `go run ./examples/mms/browse` |
| 定义、读写和删除AA命名变量表 | [MMS列表](mms/named-list/main.go) | `go run ./examples/mms/named-list` |
| 服务端主动报告，客户端接收 | [MMS报告](mms/report/main.go) | `go run ./examples/mms/report` |
| 发布状态false→true，并接收两个状态 | [GOOSE](goose/publish-subscribe/main.go) | `go run ./examples/goose/publish-subscribe` |
| 更新测量值，发送两个连续采样 | [SV](sv/publish-subscribe/main.go) | `go run ./examples/sv/publish-subscribe` |
| 按偏移组织整数、浮点、Quality和UTC | [SV布局](sv/data-layout/main.go) | `go run ./examples/sv/data-layout` |

MMS前四个示例默认自动创建回环服务，结束时关闭。加`-address 127.0.0.1:8102`可使用先启动的`go run ./cmd/iedlab protocol mms-server`；示例依赖该服务提供的sample变量。报告示例包含本机两端。

GOOSE/SV示例用内存FrameIO来展示真实编解码和对象调用，无需网卡权限。要接真实Ethernet，查看API手册中的ethernet.Open代码，或使用 [cmd工具](../cmd/README.zh-CN.md)。

## 代码里哪些是演示辅助

`internal/mmsdemo/server.go`只创建MMS本机对端，`internal/memory/link.go`只提供内存FrameIO。业务代码不需要复制这些辅助层：保留main.go中Dial之后的调用，或把内存链路替换成网卡Conn。

旧的综合断言流程已从示例入口移出；协议边界和异常路径由各包的`*_test.go`验证。示例关注应用怎样调用API。
