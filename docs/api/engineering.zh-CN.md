# 从工程文件接入设备和站控系统

[English](engineering.md)

[选点](#按一次设备找测点) · [报告服务](#由-scd-启动报告服务) · [BRCB 续传](#断线后续传-brcb) · [控制与定值](#接执行器普通参数和定值区) · [整站](#浏览器和-cli-启动完整站点) · [在线现值](#在线发现报告历史与现值) · [网络与绑定](#工程网络与自定义设备逻辑)


从仓库根目录运行两个完整示例。

```sh
go run ./examples/scd/station
go run ./examples/ied/settings
```

第一个示例共用一份 SCD，包含合并单元 MUA、间隔 IED BayIED 和站控端 StationHMI。
MUA 发出 SV，BayIED 按 SMV ExtRef 取得采样并计算三相有功功率，站控端通过 MMS
读取 MMXU1.TotW。站控端发起分合闸时，虚拟执行器更新独立位置反馈，生成 URCB
报告并通过 GOOSE 上送给 StationHMI 的工程输入。替换波形和执行器回调即可接自己的驱动。

## 按一次设备找测点

```go
system, err := scl.ParseFile("examples/scd/station/station.scd")
if err != nil { return err }
catalog, err := system.Catalog("BayIED", "S1")
if err != nil { return err }
for _, point := range catalog.Points {
    for _, equipment := range point.Equipment {
        if equipment == "Demo/E1/Q01/Q0" {
            fmt.Println(point.Reference, point.FC, point.Type,
                point.Quality, point.Timestamp)
        }
    }
}
```

`System.Equipment` 保留站、电压等级、间隔、设备路径及其 LNode 关联。
Catalog 测点包含 CDC、原始 SCL 类型、枚举标签、说明、声明的单位结构，以及最近的
兼容 q/t 属性。不支持的 DO 形成诊断，其他 DO 继续展示。`System.Device` 创建可执行
模型，要求选定 IED 的声明完整且位于运行时支持范围内。

`catalog.CheckOnline(ctx, client.MMS())` 会逐项核对在线类型和静态数据集成员顺序。
`scl.CheckDataSet` 可单独核对一个数据集。只有设备地址时，先使用
`client.MMS().Names` 和 `GetVariableAccessAttributes` 浏览在线目录与类型。

## 由 SCD 启动报告服务

```go
ctx := context.Background()
model, err := system.Device("BayIED", "S1")
if err != nil { return err }
runtime, err := simulator.Start(ctx, model, simulator.Options{
    Listen: "127.0.0.1:8102", GOOSE: simulator.GOOSEMemory,
})
if err != nil { return err }
defer runtime.Close()

client, err := ied.Dial(ctx, runtime.Address(), ied.Config{})
if err != nil { return err }
defer client.Close()
subscription, err := client.SubscribeURCB(ctx,
    mms.DomainName("BayIEDCTRL", "LLN0$RP$events01"),
    ied.ReportOptions{GI: true})
if err != nil { return err }
defer subscription.Close()
```

`model.Reports()` 列出实际可订阅的索引实例，例如 events01、history01。
ReportControl、RptEnabled.max、FCDA 顺序、confRev、TrgOps、OptFields、bufTime
和 intgPd 均来自 SCD。采集时用 `Signal.Sample` 和 `model.Update` 一起提交值、
品质和源时间。DA 的 dchg/qchg/dupd 决定事件包含原因，总召和完整性报告包含全部成员。
同一成员再次变化时先生成前一次事件，再记录新值。报告按协商 PDU 大小分段，
客户端完成校验和重组后才交付给应用。

URCB 由一个 MMS 关联占用。BRCB 在 IED 模型内保留有界事件队列，关联断开、
同模型重启传输后仍可续传。默认每个控制块 256 条，可在采集和服务启动前调用
`SetReportBufferCapacity` 设为 1～4096。模型和缓冲区由当前应用进程持有。

## 断线后续传 BRCB

```go
subscription, err := client.SubscribeBRCB(ctx,
    mms.DomainName("BayIEDCTRL", "LLN0$BR$history01"),
    ied.ReportOptions{ResumeEntryID: lastHandledEntryID})
if err != nil { return err }
defer subscription.Close()
for event := range subscription.Events() {
    if event.Err != nil { return event.Err }
    if event.Report.Reset { handleHistoryGap() }
    if err := storeEvents(event.Report.Entries); err != nil { return err }
    lastHandledEntryID = append([]byte(nil), event.Report.EntryID...)
}
return subscription.Err()
```

应用实现 `storeEvents`、`handleHistoryGap` 并保存 lastHandledEntryID。
处理完整报告后再持久化游标。省略游标沿用服务器当前发送位置，八字节全零游标
从保留队列开头读取。过期或未知游标会返回错误。BufOvfl 映射为 BufferOverflow
和 Reset，应用据此记录历史缺口。`PurgeBuffer: true` 显式清除该 BRCB 的历史，
与 ResumeEntryID 互斥。`subscription.Interrogate(ctx)` 可再次总召。

SequenceNumber 保留 URCB 的 8 位和 BRCB 的 16 位序号，Sequence 供既有 URCB
调用者使用。报文携带的可选字段逐一校验，省略时沿用订阅建立时读取的数据集和版本。
TimeOfEntry 是报告时间，对象 t 是源事件时间，ReceivedAt 是本机接收时间。

每次事件都需要同时取得值、品质和源时间时，数据集可包含完整 ST FCD。
示例 history01 使用 dsEvents 实现这一点。`report.Leaves()` 按在线声明展开为
同一报告中的标量属性。订阅建立时读取成员类型，收到报文后先校验再交付。
需要保存续传游标时，在 BRCB 的 OptFlds 中配置 EntryID。

## 接执行器、普通参数和定值区

`OnOperate` 保留普通直接二进制控制接口。`OnControl` 支持六字段 SPC/DPC Oper
对应的 ctlModel 1～4，处理选择占用、SBO/SBOw、超时、取消和增强命令终结。
SCL 中需声明对应 SBO/SBOw 和 Cancel。回调执行设备动作、请求的检查，并上送实际反馈。

需要单独展示选择步骤时，调用 `control.Select(ctx, value, options)`，然后调用
`selection.Operate(ctx)` 或 `selection.Cancel(ctx)`。一次选择使用一次。
`client.Operate` 仍可自动完成选择与操作。通过 ST 回讯确认实际位置。

普通 SP 参数通过 `model.OnSetpoint(name, callback)` 注册，回调先校验并应用设备配置。
回调拒绝时保留原值，成功后提交模型值。

定值区使用 `client.SettingGroups` 查询，使用 `client.EditSettings` 编辑、确认并选择是否激活。
值映射使用 SE 叶属性名。返回的 Committed、Activated 分别记录确认和激活证据。
编辑备用区期间，SG 继续显示运行区。编辑预约在 60 秒或关联断开后释放。
[完整示例](../../examples/ied/settings/main.go) 展示普通 SP、备用组确认和显式激活。

## 在软件中连接多台 IED

多个运行时共用 `simulator.MemoryBus`，每个端点具有独立读者和有界队列，GOOSE
与 SV 通过完整以太网帧交换数据，包含 VLAN。端点队列满时广播返回明确错误。
停止时先取消并等待读者退出，再注销订阅和关闭链路。

`System.SVInputs` 列出 SMV ExtRef，`System.SVInput` 按接收方 intAddr 找到源流、
FCDA 字段及相关品质。GOOSE 对应 `System.GOOSEInputs` 和 `IED.Input`。
`simulator.Options.SV` 在共享总线上启动软件采样源，`RunSampledValues` 也接受
调用方持有的以太网 FrameIO。SampleSync 默认 0，示例显式接受软件时钟，并按该
工程约定使用倍率。Go 定时器提供演示节拍，物理采样、同步和性能由设备时钟及现场测量验证。

示例已按 2007B4 XSD 验证结构。工程、服务和模型依据可查
[IEC 61850-6](https://webstore.iec.ch/en/publication/103863)、
[IEC 61850-7-2](https://webstore.iec.ch/en/publication/66525)、
[IEC 61850-7-3](https://webstore.iec.ch/en/publication/66526) 和
[IEC 61850-7-4](https://webstore.iec.ch/en/publication/66551)。


## 浏览器和 CLI 启动完整站点

运行 `go run ./cmd/iedlab serve`，点击“整站示例”，再点击“启动整站”。每个服务接入点有独立的回环 MMS 监听地址，多个 IED 共享软件总线。站点面板显示 GOOSE/SMV ExtRef 输入及采样计数。编辑 MUA 的采集值，SV 接收端会看到后续样本变化；通过 MMS 操作 BayIED，可在 HMI 的 GOOSE 输入看到位置反馈。“停止整站”会等待全部收发任务退出后释放总线。

无界面方式：

```sh
go run ./cmd/iedlab simulate -station -file examples/scd/station/station.scd
```

嵌入程序使用 `simulator.StartStation(ctx, system, simulator.StationOptions{})`，通过 `station.Devices()` 取得各模型和监听地址，通过 `station.Inputs()` 或 `StationOptions.Observe` 获取解码后的输入。

默认 `ModelSVSource` 将 MU 数据集的一致快照编码成 SV，包括 Quality 标志转换。它发布模型当前值，不推断比例、波形、同步时钟或设备算法。可通过 `StationOptions.Sources[profile.Reference]` 换成自定义采样回调。IntAddr 是应用输入名，计算或控制逻辑需显式更新目标模型；完整示例中的波形和功率计算仍是应用代码。

## 定值变化、数组和控制终结

SP 写入及活动 SG 变更现在与采集共用一致快照和发布入口，能够触发报告与 GOOSE。编辑未激活定值组不会发布活动值变化。只读子属性也保护对整个父结构或数组的写入。

DO/SDO/DA/BDA 数组支持 1..4096 个元素，并受总展开预算限制。DOI/SDI/DAI 的 `ix` 从零开始；FCDA 的 `component(index)` 引用保留真实嵌套 MMS 交替访问。更新整个不可变数组后，变化报告区分成员索引以及值变和品质变。客户端用 `mms.Selection.Path` 表示有序组件/索引路径。

未绑定数据集的 ReportControl 可以检查，但不能启用。禁用时可将 DatSet 切到同一逻辑设备的静态数据集，同时增加 ConfRev。BRCB 换数据集前需显式清空旧缓冲。动态创建的 MMS 变量列表暂不能绑定为报告数据集。

增强二进制控制先回复 Operate 受理成功，再调用 OnControl 执行设备动作并返回正/负 CommandTermination。回调失败不再混成受理拒绝；客户端能够区分 Acknowledged 与 Terminated。执行上下文随关联关闭取消，普通安全控制仍同步执行回调。

## 在线发现、报告历史与现值

通过 `client.Discover(ctx, ied.DiscoveryOptions{})`，仅使用地址就能发现实际
LD、类型、数据集、RP/BR 和 SGCB。检查 Diagnostics 与 Truncated 判断完整性。
一次设备、网络和 ExtRef 信息由 SCL 提供。

`subscription.ReadSnapshot(ctx)` 与 `client.ReadDataSet(ctx, name)` 独立读取
当前数据集，调用 Leaves() 展开 FCD 中的值、品质和时间。BRCB 报告中的 GI
原因可能来自以前的会话。现值快照没有 EntryID，不推进历史归档游标。
不同 FCDA 的采集一致性取决于对端，相关值、q、t 优先采用一个 FCD 成员。
内置 Model 和 SCL IED 已实现同一采集快照的批读，自定义存储可实现
mms.BatchReader，读取装饰器需要明确转发这个能力。

## 工程网络与自定义设备逻辑

`system.Project()` 保留子网、AP/ServerAt、一次设备及支持范围内的语义诊断。
`system.Inputs(ied, ap)` 保留接收侧 LD/LN 和声明序号。InputReference.Key()
允许 intAddr 省略或跨 LN 重用，ResolveGOOSEInput/ResolveSVInput 直接解析
这个引用。旧 intAddr 查询方法继续可用。CompareCatalog 对照类型、单位、
q/t 绑定、FCDA 顺序与选择路径、报告版本和输入源。ReportSettings 的
Fix/Conf/Dyn 声明决定 RCB 字段能否在线修改。这些检查需配合对应版本的
外部 XSD/NSD 验证。

StartStation 按 SubNetwork 隔离总线，ServerAt 表达同一服务器的另一物理端口。
StrictInputs=true 时未绑定或不连通的输入会阻止启动。默认模式保留输入诊断。
OnSample 每次交付同一 ASDU 的全部输入，包含未变化的样本、超时与恢复事件。
OnGOOSE 交付状态事件，Observe 用于界面变化通知。回调及时返回，由拥有者关闭
站级运行。完整实现见 examples/scd/station，业务计算和执行器可替换。

InputReading 分别保留最后数据、品质、接收时间、Valid、State、ValidUntil
和 LastGoodAt。非零品质保守标记为不可用并保留全部品质位，SampleSync 独立
展示。GOOSE 使用 TAL，SV 使用 InputTimeout，NominalFrequency 支持 50/60 Hz。
