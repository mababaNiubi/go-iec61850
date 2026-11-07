# 一台断路器 IED 和一套站端监控

[English](README.md) | 简体中文

在仓库根目录运行以下命令。MMS 使用临时本机 TCP 端口，两端实际收发 MMS 请求。GOOSE 帧经编解码后通过内存 Ethernet 链路传递。

```sh
go run ./examples/scd/breaker
```

```text
GOOSE BayIEDCTRL/XCBR1$ST$Pos$stVal stNum=1 position=open
MMS active power=0 W position=open
MMS close blocked by interlock
MMS active power=1250 W position=closed
GOOSE BayIEDCTRL/XCBR1$ST$Pos$stVal stNum=2 position=closed
MMS active power=0 W position=open
GOOSE BayIEDCTRL/XCBR1$ST$Pos$stVal stNum=3 position=open
```

| 文件 | 自己接入时改什么 |
|---|---|
| [bay.scd](../../../internal/sample/bay.scd) | 设备名、AP、LD、LN 模板和测点，数据集 FCDA 顺序、GSEControl、Communication/GSE 和接收方 ExtRef |
| [process.go](process.go) | 将硬件信号绑定到点名，用自己的驱动和闭锁规则实现 `processIO.Read` 与 `Operate` |
| [monitor.go](monitor.go) | MMS 轮询、发送完整 Oper、按 ExtRef 取订阅值、处理 GOOSE 失效 |
| [main.go](main.go) | 选择 IED/AP，启动服务端、发布和订阅，协调退出 |

## 设备和监控分开跑

终端一启动模拟 IED，终端二启动监控。两者默认使用 SCD 里的回环地址和 8102 端口，可以部署在同一台电脑。监控端会持续轮询，按 Ctrl+C 退出。

```sh
go run ./examples/scd/breaker -role ied
```

```sh
go run ./examples/scd/breaker -role monitor
```

如需向该模拟 IED 发一次命令，启动监控时额外指定 `-operate close` 或 `-operate open`。默认只监控。独立 IED 启动时允许合闸。要让两个进程收发 GOOSE，在**两条命令**上各自加 `-interface <网卡名>`，并使用能传递 SCD 中组播和 VLAN 的二层链路。两端可以在不同主机上使用各自的网卡。`-address host:port` 可覆盖 MMS 监听或连接地址，Windows 接收侧可按需要加 `-promiscuous`。详见[平台说明](../../../docs/platforms.zh-CN.md)。

## 换成自己的模型

先查看文件里有哪些 IED、接入点和路径。

```sh
go run ./cmd/iedlab inspect -file internal/sample/bay.scd
go run ./cmd/iedlab inspect -file internal/sample/bay.scd -ied BayIED -ap S1 -match XCBR1.Pos
```

相同业务信号的工程可传 `-file your.scd -ied <设备> -ap <接入点> -ld <逻辑设备实例>`。如果点表不同，按新设备的 LN/DO/DA 修改 `bindPoints`，并把示例的 `gcbPos` 和 `StationHMI/S1/breakerPosition` 换成实际控制块和接收方 ExtRef。公开 `scl` 包负责解析引用和报文字段。[SCD API 指南](../../../docs/api/scl.zh-CN.md)给出了常用路径、设备与监控端调用和支持范围。
