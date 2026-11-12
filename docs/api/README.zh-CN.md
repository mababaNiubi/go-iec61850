# API 使用手册

[工程场景](engineering.zh-CN.md) 包含一次设备选点、建模上送、BRCB 续传、定值操作和完整软件站点。安装：`go get github.com/mababaNiubi/go-iec61850@v1.4.0`，要求 Go 1.24+。

[English](README.md) | 简体中文

按正在做的事情选一项，代码可复制到应用中。每页都说明需要的对象、参数、返回结果和对应示例。

| 你要做什么 | 直接看这里 | 可运行代码 |
|---|---|---|
| 仅用地址发现设备，读取当前数据集 | [在线发现与现值](engineering.zh-CN.md#在线发现报告历史与现值) | [GoDoc 可执行示例](../../ied/example_test.go) |
| 检查子网和 ServerAt、对比工程、绑定 ExtRef | [工程网络与业务逻辑](engineering.zh-CN.md#工程网络与自定义设备逻辑) | [完整站点](../../examples/scd/station/main.go) |
| 续传 BRCB 历史，编辑确认和激活定值组 | [工程场景](engineering.zh-CN.md) | [settings](../../examples/ied/settings/main.go) |
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

## 不用 SCL 发现设备并读取现值

下面函数只需要设备地址，演示选择发现的第一个数据集；正式程序应选择业务需要的数据集。可先运行 `go run ./examples/scd/station` 查看本机设备地址，也可以连接真实 IED。

```go
import (
    "context"
    "fmt"
    "time"

    "github.com/mababaNiubi/go-iec61850/ied"
)

func inspect(address string) error {
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    client, err := ied.Dial(ctx, address, ied.Config{})
    if err != nil { return err }
    defer client.Close()

    directory, err := client.Discover(ctx, ied.DiscoveryOptions{MaxObjects: 4096})
    if err != nil { return err }
    if directory.Truncated || len(directory.Diagnostics) != 0 {
        fmt.Println("发现结果不完整：", directory.Truncated, directory.Diagnostics)
    }
    if len(directory.DataSets) == 0 { return fmt.Errorf("没有在线数据集") }
    snapshot, err := client.ReadDataSet(ctx, directory.DataSets[0].Name)
    if err != nil { return err }
    leaves, err := snapshot.Leaves()
    if err != nil { return err }
    for _, leaf := range leaves {
        // 数组成员的身份需要同时保留 Selection。
        fmt.Println(leaf.Variable.Name, leaf.Variable.Selection, leaf.Value)
    }
    return nil
}
```

`Discover` 返回声明，不读取现值；检查 `Diagnostics` 和 `Truncated`。`ReadDataSet` 先读取成员类型，再在一个 MMS Read 中取得数据；任一成员失败会返回错误。使用 `Bool`、`Int`、`Uint`、`Float`、`Time` 转换值并处理类型错误。[GoDoc 示例](../../ied/example_test.go) 会随测试编译和执行。

## 把自己的逻辑接入 SCL

1. `scl.ParseFile(path)` 读取工程；`system.Project()` 和 `system.Catalog(iedName, apName)` 离线检查支持的声明和诊断，不创建连接。
2. `system.Device(iedName, apName)` 构建独立模型。用 `model.Lookup`、`Signal` 解析路径；通过 `Signal.Sample(value, quality, timestamp)` 生成更新，在一次 `model.Update(ctx, changes...)` 中提交同次采集。
3. 启动前注册 `model.OnControl`，调用自己的驱动并通过独立采集更新 ST 反馈。`OnOperate` 保留普通直接二进制控制接口。
4. `simulator.Start` 管理单个设备；`simulator.StartStation` 按 SubNetwork 连接整站，通过 `StationOptions` 传入模型、采样源和 `OnSample`、`OnGOOSE` 逻辑。
5. 监控程序独立使用 `ied.Dial`、报告和现值读取。有工程文件时，用 `catalog.CheckOnline(ctx, client.MMS())` 校对在线类型和数据集，再复用映射。

完整代码见[整站程序](../../examples/scd/station/main.go)和[工程场景](engineering.zh-CN.md)。支持范围内的语义诊断不能替代对应版次 XSD 或完整 NSD 校验。

## 名称、生命周期与结果约定

| 约定 | 调用方怎么处理 |
|---|---|
| 包选择 | `mms` 做原始读写；`ied` 做控制、报告、发现、定值；`scl` 做工程模型；`simulator` 管理运行；`data` 做类型值；`goose`、`sv`、`ethernet` 做二层传输。业务程序无需导入工作台。 |
| MMS 名称 | `mms.DomainName("BayIEDCTRL", "XCBR1$ST$Pos$stVal")` 对应 `ied.ObjectName("BayIEDCTRL", "ST", "XCBR1.Pos.stVal")`。Domain、FC 取自模型或设备。 |
| 模型路径 | `model.Lookup("CTRL", "XCBR1", "ST", "Pos.stVal")` 处理 LD 别名并检查声明，避免自己拼接 Domain。 |
| 超时 | `Dial` 的 ctx 限制连接；之后每次请求单独传 ctx。`mms.Config.RequestTimeout` 为零时，每次确认请求默认 5 秒。 |
| 关闭 | 构造成功后立即 `defer Close()`。先停订阅再关客户端。`ied.Client` 独占底层报告分发，不要再读 `client.MMS().Reports()`。 |
| 订阅 | 建立 ctx 不决定订阅寿命；使用 `Stop(ctx)`、`Close()` 或关闭客户端停止。及时消费 `Events()`，通道关闭后检查 `Err()`。 |
| 批量错误 | `ReadMultiple`、`ReadNamedList` 同时检查请求错误和逐项 `ReadResult.Error`。事务成功不代表所有成员成功。 |
| 现值与历史 | `ReadDataSet`、`ReadSnapshot` 没有 EntryID。完整 BRCB 报告经业务持久处理后才保存 EntryID。GI 没有请求关联号。 |
| 控制 | 错误时也检查 `Selected`、`Acknowledged`、`Terminated`；物理动作通过 ST 确认。不自动重发结果不确定的命令。 |
| 品质与时间 | 分别保留 q、源 t、本地接收时间；超时后 Value 可能仍保留旧值，使用前检查 `InputReading.Valid`、`State`。倍率和品质接纳策略由业务决定。 |
| 回调 | 回调同步运行在工作线程，不同流可能并发。及时返回，保护共享业务状态，由拥有者发起关闭。 |
| 二层与时钟 | GOOSE/SV 需要网卡或 `FrameIO`，示例默认内存链路；软件站点不提供同步的物理采样时钟。 |

## 查询准确签名与注释

```sh
go doc github.com/mababaNiubi/go-iec61850/ied.Client.Discover
go doc github.com/mababaNiubi/go-iec61850/ied.ReportOptions
go doc github.com/mababaNiubi/go-iec61850/simulator.StationOptions
go doc -all github.com/mababaNiubi/go-iec61850/scl
go test ./ied -run Example -v
```

GoDoc 注释紧邻公开声明，说明默认值、单位、范围、所有权及错误含义。工具调用时结合真实签名与本页场景选择接口。`internal/` 是工作台内部实现。
