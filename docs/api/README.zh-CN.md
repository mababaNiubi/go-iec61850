# API 使用手册

[工程场景](engineering.zh-CN.md) 包含一次设备选点、建模上送、BRCB 续传、定值操作和完整软件站点。这些新增接口位于当前开发源码。

[English](README.md) | 简体中文

按正在做的事情选一项，代码可复制到应用中。每页都说明需要的对象、参数、返回结果和对应示例。

| 你要做什么 | 直接看这里 | 可运行代码 |
|---|---|---|
| 按 ctlModel 控制、订阅 URCB、嵌入模拟器、解析 SV 布局 | [设备 API](ied.zh-CN.md) | [control-report](../../examples/ied/control-report/main.go)、[SCL/SV](../../examples/sv/scl/main.go) |
| 使用网页工作台或命令行调试 IED | [IEDLab 工作台](../workbench.zh-CN.md) | [应用入口](../../cmd/iedlab/main.go) |
| 解析 SCD、查询常用路径、接入采集和控制、按 ExtRef 订阅 | [SCD 模型接入](scl.zh-CN.md) | [断路器和监控站](../../examples/scd/breaker/README.zh-CN.md) |
| 连接设备，读写一个变量 | [MMS：连接与读写](mms.zh-CN.md#连接设备并读写一个整型变量) | [read-write](../../examples/mms/read-write/main.go) |
| 一次读取多个变量，处理部分失败 | [MMS：批量读取](mms.zh-CN.md#一次读取多个变量) | [read-batch](../../examples/mms/read-batch/main.go) |
| 找到设备中的变量名和类型 | [MMS：目录与类型](mms.zh-CN.md#发现-domain变量名和类型) | [browse](../../examples/mms/browse/main.go) |
| 读取数组/结构中的一部分 | [MMS：元素与成员](mms.zh-CN.md#读取数组元素或结构成员) | [read-batch](../../examples/mms/read-batch/main.go) |
| 把多个变量组织成命名列表 | [MMS：命名变量表](mms.zh-CN.md#按命名变量表成组读写) | [data-set](../../examples/mms/named-list/main.go) |
| 接收服务器主动报告 | [MMS：报告](mms.zh-CN.md#接收和发送-informationreport) | [report](../../examples/mms/report/main.go) |
| 创建自己的 MMS 服务端 | [MMS：服务端](mms.zh-CN.md#创建自己的-mms-服务端) | [本地示例服务](../../examples/internal/mmsdemo/server.go) |
| 把 MMS Value 转成 Go 类型 | [值与类型](data.zh-CN.md) | [read-write](../../examples/mms/read-write/main.go) |
| 发布和订阅开关量状态 | [GOOSE API](goose.zh-CN.md) | [publish-subscribe](../../examples/goose/publish-subscribe/main.go) |
| 发布、订阅或解析采样值 | [SV API](sv.zh-CN.md) | [publish-subscribe](../../examples/sv/publish-subscribe/main.go) |
| 定义 SV 数据的字节布局 | [SV：采样布局](sv.zh-CN.md#自己定义采样值布局) | [data-layout](../../examples/sv/data-layout/main.go) |

导入路径：`github.com/mababaNiubi/go-iec61850/mms`、`data`、`goose`、`sv`、`ethernet`、`scl`、`ied`、`simulator`。MMS与GOOSE/SV使用不同传输：MMS连TCP地址，GOOSE/SV需要Ethernet接口或实现FrameIO。

[示例运行方法](../../examples/README.zh-CN.md) · [现成命令行工具](../../cmd/README.zh-CN.md)
