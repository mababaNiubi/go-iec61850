# 设备客户端与可嵌入模拟器

[English](ied.md) | 简体中文

`ied` 提供 IEC 61850 控制和 URCB 报告语义；已有 `mms` API 继续用于底层 MMS 服务。`simulator` 让其他 Go 程序在进程内运行 SCL 设备，不依赖 IEDLab 或 HTTP 服务。这些公开接口从 v1.2.0 引入。

## 连接、读取、控制

在返回 `error` 的函数中使用，导入标准库 `context`、`time` 和公开的 `data`、`ied` 包：

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

client, err := ied.Dial(ctx, "127.0.0.1:8102", ied.Config{
    Origin: ied.Origin{Category: 2, ID: []byte("my-station")},
})
if err != nil { return err }
defer client.Close()

name, err := ied.ObjectName("BayIEDCTRL", "MX", "MMXU1.TotW.mag.f")
if err != nil { return err }
value, err := client.Read(ctx, name)
if err != nil { return err }
watts, err := value.Float()
if err != nil { return err }
_ = watts // 接到自己的业务处理

result, err := client.Operate(ctx, "BayIEDCTRL/XCBR1.Pos", data.Boolean(true),
    ied.ControlOptions{InterlockCheck: true})
if err != nil { return err }
_ = result.Acknowledged
```

`Config.MMS` 接受现有 `mms.Config`，包括 ISO 选择子和请求超时。有 SCL 模型时传入设备的 `ClientConfig()`。Dial 的 ctx 控制建连；后续请求各自传 ctx。Close 释放关联和报告订阅。

`Operate` 在线读取 CF.ctlModel 和 CO.Oper 类型，按设备声明的字段顺序构造完整结构，支持可选的 ctlNum、operTm。会话串行处理选择与执行，调用方不必另加互斥锁。

| ctlModel | 实际流程 |
|---|---|
| 1 / `DirectNormal` | 写 Oper |
| 2 / `SBONormal` | 读 SBO 选择，再写 Oper |
| 3 / `DirectEnhanced` | 写 Oper，等待匹配的命令终结 |
| 4 / `SBOEnhanced` | 写 SBOw 选择，再写 Oper 并等待终结 |

当前是即时操作，不调度未来 operTm。Test、联锁检查和同期检查通过选项明确传入，由 IED 决定如何执行。

非二进制控制先调用 `client.Control(ctx, "LD/LN.object")`，再用 `control.ValueType()` 查询 ctlVal 类型，构造对应的 `data.Integer`、`Unsigned`、`Float32` 等值后调用 `control.Operate`。设备重新配置后重新取得 Control 对象。

`ControlResult.Selected`、`Acknowledged`、`Terminated` 分别表示选择、MMS 确认和增强控制终结。实际位置应单独读取或订阅 ST 反馈。错误保留原始原因，可用 `errors.Is` 判断：

| 错误 | 处理方式 |
|---|---|
| `ied.ErrNotSubmitted` | Oper 尚未提交，检查建连、选择等前置错误 |
| `ied.ErrRejected` | 装置或本地类型检查明确拒绝 |
| `ied.ErrUnconfirmed` | 提交或终结不确定，先核对反馈，再决定后续操作 |

库不会自动重发结果不确定的控制。[可运行的控制与报告示例](../../examples/ied/control-report/README.zh-CN.md)

## 订阅已配置的 URCB

使用分配给当前客户端的报告控制块实例，沿用工程配置的数据集、触发选项和 OptFlds。报文携带的数据集和版本逐项校验，分段报告重组完成后交付。`SubscribeBRCB` 可按 EntryID 续传缓冲历史。建模上送、续传和定值操作见[工程 API](engineering.zh-CN.md)。

```go
block, err := ied.ObjectName("LD", "RP", "LLN0.Events01")
if err != nil { return err }
sub, err := client.SubscribeURCB(ctx, block, ied.ReportOptions{Buffer: 64})
if err != nil { return err }
defer sub.Close()

for {
    select {
    case <-ctx.Done():
        return ctx.Err()
    case event, ok := <-sub.Events():
        if !ok { return sub.Err() }
        if event.Err != nil {
            // 清除旧缓存，按应用策略轮询或重新绑定。
            continue
        }
        if event.Report.Reset {
            // 序号不连续：先清除以前报告缓存的成员。
        }
        for _, entry := range event.Report.Entries {
            _ = entry.Variable.Name
            _ = entry.Value
            _ = entry.Reason
        }
    }
}
```

配置阶段在支持时预留 RCB，并在启用 RptEna 前注册接收，避免丢掉提前到达的报告。已启用的 RCB 或同关联内重复的 RptID 会被拒绝。配置 ctx 只约束建立订阅；调用 `Stop(ctx)`、`Close()` 或关闭客户端结束订阅。启用／释放结果不确定时关闭关联，释放本关联的占用。

整份报告校验完成后才提供成员。要求一致采集时，值与 q 应来自同一份报告。初值可先通过 confirmed Read 获取；订阅不会强制 GI，也不保证立即产生报告。缓存有效期和回退轮询由应用决定。

消费者过慢会关闭自身队列，`Err()` 返回 `ErrReportOverflow`，不会阻塞控制终结或其他订阅。`client.MMS()` 可调用高级 confirmed 服务；**不要读取其原始 Reports() channel**，该通道由 `ied.Client` 唯一消费和分发。

## 品质与数值由调用方决定怎么用

`ied.DecodeQuality` 将 MMS/GOOSE 的 13 位 Quality 转成 `ied.Quality`，标志编号与 SV 四字节品质字的低位编号一致；`q.MMSValue()` 可转换回去。`GoodValidity()` 只检查 validity，应用再通过 `Has(ied.OldData | ied.Test | ...)` 决定能否使用。SV 品质字可先转 `ied.Quality`，用 `Valid()` 检查保留高位。

`data.Value` 保留有符号／无符号整数、Float32 原始宽度、UTC 品质字节。Unix 毫秒转换、浮点舍入、工程量换算和品质使用策略留在应用。`data.ResolveComponent`、`ValueAt`、`WithComponent` 可定位结构和从零开始的数组元素，修改时保留其他成员。

## 在其他程序中运行模拟设备

```go
system, err := scl.ParseFile("device.scd")
if err != nil { return err }
model, err := system.Device("BayIED", "S1")
if err != nil { return err }

// 在这里注册 model.OnOperate 回调，并设置初始采集值。
runtime, err := simulator.Start(ctx, model, simulator.Options{
    Listen: "127.0.0.1:8102",
    GOOSE: simulator.GOOSEMemory,
})
if err != nil { return err }
defer runtime.Close()
return runtime.Wait(ctx)
```

Start 管理 TCP 监听、连接、帧链路和后台协议任务；端口填 0 时用 `Address()` 取得实际地址。父 ctx 取消或 `Stop(ctx)` 会停止传输，模型仍可更新并重新启动。采集使用 `model.Update`，执行使用 `model.OnOperate` 接自己的驱动。当前建模服务端支持普通直接 SPC/DPC 回调；客户端支持四种控制模型，不代表模拟服务端自动支持全部模型。

Memory 模式实际编码、发布并接收 GOOSE 帧。Ethernet 模式指定 Interface 并使用实际网卡 MAC。Observe 是并发事件回调，应快速返回，关闭操作放在回调外。Store 装饰器可记录 MMS 访问。MemoryBus 连接独立运行时，Options.SV 可在共享总线上启动软件采样源。参见[完整站点示例](../../examples/scd/station/README.zh-CN.md)。

## 从 SCL 获得 SV 布局，共用一个网卡读取循环

`system.SVStream("MUA/AP1/LD0/MSVCB01")` 返回 `sv.Profile`；`SVStreams(ied, ap)` 枚举该接入点的流，无需先构建 MMS 运行模型。`profile.Layout.Field(member, path)` 按 FCDA 名或序号定位标量，`QualityFor(field)` 查同数据对象的 q。`Field.Decode/Encode` 使用原始 SCL 类型和已检查偏移，包括 SV 中四字节的 Enum、Dbpos、Quality、Check。

用 `profile.SubscriptionConfig(sv.SubscriptionOptions{})` 创建 Subscriber。每条流向同一个 `sv.Receiver` 注册 Subscriber、`profile.ValidateFrame` 和结果回调；同一链路只调用一次 `Run(ctx, link)`。[可运行的 SCL/SV 示例](../../examples/sv/scl/README.zh-CN.md)

`goose.Receiver` 提供同样的多流分发。回调运行在接收循环上，应快速处理或放入应用自己的有界队列。Receiver 回调报告收包结果；停止来包后的过期状态由应用定时／采集循环调用 `Snapshot()` 或 `CheckTimeout()` 检查。`Subscriber.Events()` 属于单流 `Subscriber.Run` 用法，Receiver 不会重复填入该 channel。先取消并等待 Run 返回，再解除注册和关闭链路；解除注册会等待当前分发，应在回调外执行。

GOOSE 数据集可使用 SCL 流的 `stream.Layout()` 和 `stream.Values(ctx)`。`layout.Field(member, path)` 定位 allData 字段，`Read/Set` 保留结构内其他成员。复用点表前可用 `CheckCompatible` 比较成员顺序、逻辑设备关系和声明类型；`LeafCount` 可用于发布初始值完整性策略。

## 网关适配边界

通道管理、重试策略、点表模板、单位倍率、数据库对象、业务时钟和快照、variant 转换及控制结果映射留在网关。协议行为调用公开的 `ied`、`scl`、`data`、`goose`、`sv`、`simulator`。库不反向依赖网关，也不引入 variant 等第三方运行依赖。IEDLab 已使用相同的 `ied` 和 `simulator` API。
