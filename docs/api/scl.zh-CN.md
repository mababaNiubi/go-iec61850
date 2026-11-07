# 从 SCD 接入设备和监控系统

[English](scl.md) | 简体中文

先从仓库根目录运行[断路器示例](../../examples/scd/breaker/README.zh-CN.md)。[SCD 文件](../../internal/sample/bay.scd)定义了间隔 IED、监控 IED 和站控层网络。两端共享同一套点名、类型和数据集顺序。

```sh
go run ./examples/scd/breaker
```

监控端先收到断路器分位，通过 MMS 读到 0 W。第一次合闸被闭锁拒绝。解除闭锁后再次合闸，MMS 读到 1250 W 和合位，GOOSE 也推送合位。最后分闸，两种通道均回到分位。

## 先查 SCD 的接入点和常用路径

```sh
go run ./cmd/iedlab inspect -file internal/sample/bay.scd
go run ./cmd/iedlab inspect -file internal/sample/bay.scd -ied BayIED -ap S1 -match XCBR1.Pos
go run ./cmd/iedlab inspect -file internal/sample/bay.scd -ied BayIED -ap S1 -ref BayIEDCTRL/MMXU1.TotW.mag.f -fc MX
go run ./cmd/iedlab inspect -file internal/sample/bay.scd -ied StationHMI -ap S1 -inputs
```

第一条列出 IED、接入点及其是否有 Server。第二条列出匹配的对象、静态数据集和 GOOSE 参数。第三条查询精确路径和类型。第四条列出监控端定义的 GOOSE 输入和源对象。工具离线运行，无需连接设备。

| 业务信号 | LD 实例 `CTRL` 内的对象路径 | FC | MMS 域 `BayIEDCTRL` 下的 item |
|---|---|---|---|
| 有功功率，单位 W | `MMXU1.TotW.mag.f` | MX | `MMXU1$MX$TotW$mag$f` |
| 测量品质 / 采集时间 | `MMXU1.TotW.q` / `MMXU1.TotW.t` | MX | `MMXU1$MX$TotW$q` / `MMXU1$MX$TotW$t` |
| 断路器实际位置 | `XCBR1.Pos.stVal` | ST | `XCBR1$ST$Pos$stVal` |
| 位置品质 / 最后变位时间 | `XCBR1.Pos.q` / `XCBR1.Pos.t` | ST | `XCBR1$ST$Pos$q` / `XCBR1$ST$Pos$t` |
| 合闸闭锁 / 操作次数 | `XCBR1.BlkCls.stVal` / `XCBR1.OpCnt.stVal` | ST | `XCBR1$ST$BlkCls$stVal` / `XCBR1$ST$OpCnt$stVal` |
| 控制模型 | `XCBR1.Pos.ctlModel` | CF | `XCBR1$CF$Pos$ctlModel` |
| 完整分合闸命令 | `XCBR1.Pos.Oper` | CO | `XCBR1$CO$Pos$Oper` |
| 位置数据集 | `LLN0 / dsPos` | 数据集 | `LLN0$dsPos` |

默认域名由 IED 名 `BayIED` 加 LD 实例 `CTRL` 得到。若 `LDevice.ldName` 显式配置，则使用该名字。LN 名包含前缀、类和实例号。`Resolve` 接收完整引用和功能约束 FC，`Lookup` 接收 LD 实例、LN、FC 和点分属性路径。两者都会检查对象是否在所选模型中。

## 修改模型时查哪个 XML 节点

| 想改什么 | SCL 元素路径 |
|---|---|
| 设备名、LD 名、LN 实例 | `IED/AccessPoint/Server/LDevice/LN` 或 `LN0` |
| 测点以及报文类型 | `DataTypeTemplates/LNodeType/DO` → `DOType/DA`（或 `SDO`）→ `DAType/BDA` 或 `EnumType` |
| 某个实例的初值，包括控制模型 | `LN/DOI/DAI/Val` 或嵌套的 `SDI/DAI/Val` |
| 数据集成员及顺序 | `LN0/DataSet/FCDA` |
| 发布控制块以及二层地址 | `LN0/GSEControl`，按 IED、AP、LD 实例和控制块名匹配 `Communication/SubNetwork/ConnectedAP/GSE` |
| 接收端业务输入 | `LN0/Inputs/ExtRef` 或 `LN/Inputs/ExtRef`，通过 `intAddr`、源控制块字段和源数据引用定位 |

## 监控系统读取现有设备

下面代码导入标准库 `context`，以及 `github.com/mababaNiubi/go-iec61850/` 下的 `mms` 和 `scl` 包。调用方通过 context 设置截止时间。外部应用可用 `go get github.com/mababaNiubi/go-iec61850/scl@v1.1.0` 安装。

```go
func readWatts(ctx context.Context, path string) (float64, error) {
    system, err := scl.ParseFile(path)
    if err != nil { return 0, err }
    ied, err := system.Device("BayIED", "S1")
    if err != nil { return 0, err }
    name, err := ied.Lookup("CTRL", "MMXU1", "MX", "TotW.mag.f")
    if err != nil { return 0, err }

    client, err := mms.Dial(ctx, ied.MMSAddress(), ied.ClientConfig())
    if err != nil { return 0, err }
    defer client.Close()
    value, err := client.Read(ctx, name)
    if err != nil { return 0, err }
    return value.Float()
}
```

长期运行时只解析一次，复用客户端和已解析的点名。`MMSAddress` 读取所选 ConnectedAP 的 IP/DNS 和 TCP 端口，缺省端口为 102。工程文件未配置地址时由应用提供。`ClientConfig` 从 SCD 取得对端的 T/S/P 选择子、AP title 和 AE qualifier，本端选择子可按设备要求补充。批量读要分别处理每个 `ReadResult.Error`。完整轮询见[监控代码](../../examples/scd/breaker/monitor.go)。

## 设备侧接采集和执行逻辑

设备启动时用 `power, err := ied.Signal("CTRL", "MMXU1", "MX", "TotW", "mag.f")` 绑定值、品质和时间三个路径。驱动一次采集给出实际测量、品质和时间，通过一次 `Update` 一起生效。

```go
func updatePower(ctx context.Context, ied *scl.IED, power scl.Signal,
    sample float32, quality mms.Value, acquired time.Time) error {
    return ied.Update(ctx, power.Sample(mms.Float32(sample), quality, mms.UTC(acquired))...)
}
```

示例的良好品质使用 `mms.BitString([]byte{0, 0}, 13)`。接真实驱动时传入实际品质和时间。批内任一项类型出错，整批都不会生效。网络端只读 ST/MX。示例将位置时间保留在最后一次实际变位，测量值则使用本次采集时间。

在开始监听前，通过 `OnOperate` 给 CO 下的整个 `Pos.Oper` 注册回调。回调可取得目标开关量、来源、控制号、命令时间、Test 和 Check 位。先检查闭锁和设备能力，再调用驱动并采集实际反馈。直接写 `Pos.stVal` 或 `Oper.ctlVal` 会被拒绝。异步执行的设备应在驱动确认变位时更新 ST，不能用命令应答代替位置反馈。可替换的 `processIO` 接口见[设备逻辑](../../examples/scd/breaker/process.go)，启动与关闭见[两端接入代码](../../examples/scd/breaker/main.go)。

在监控端，先解析完整 CO 对象，再发送直接控制。每条命令分配控制号，来源填写站端实际标识。写入成功后，通过 ST 读回或 GOOSE 事件确认位置。

```go
func closeBreaker(ctx context.Context, client *mms.Client, oper mms.ObjectName, number uint8) error {
    cmd := scl.Command{
        Value: true, Number: number, Time: time.Now(),
        OriginCategory: 2, OriginID: []byte("StationHMI"), InterlockCheck: true,
    }
    value, err := cmd.MMSValue()
    if err != nil { return err }
    return client.Write(ctx, oper, value)
}
```

## 监控端订阅模型里配置的 GOOSE 输入

发布端用 `ied.Stream("CTRL", "gcbPos")` 取得流，再运行 `stream.Run(ctx, link, sourceMAC)`。SCD 给出控制块 `BayIEDCTRL/LLN0$GO$gcbPos`、数据集 `BayIEDCTRL/LLN0$dsPos`、修订号 1、目标 MAC `01-0c-cd-01-00-01`、APPID `0x1001`、VID 123 和 PCP 4。真实链路用 `ethernet.Open` 和 `conn.MAC()`。发布器负责重传，采集数据改变时按 FCDA 顺序更新。若链路来不及处理 64 个不同的数据集快照，运行循环返回 `scl.ErrPublicationOverflow`。

监控端用 `input, err := ied.Input("StationHMI", "S1", "breakerPosition")` 查本端 `ExtRef.intAddr`。它定位源 GSEControl、FCDA 下标，必要时定位整 DO 里的具体属性。`input.Stream.NewSubscriber()` 建立过滤器，`sub.Run(ctx, link)` 驱动收包，业务消费 `sub.Events()`，同时观察 `Run` 的退出结果。有效事件用 `input.Value(event)` 取值，再用 `scl.ReadPosition` 解析双点位置。收到 `goose.Expired` 将显示值标记失效，有效恢复后再使用。示例在关闭网卡前等待网络循环退出。

## 工具方法速查

| 方法 | 什么时候用 |
|---|---|
| `scl.ParseFile(path)` / `scl.Parse(reader)` | 读取磁盘 SCD，或解析嵌入、上传的模型 |
| `system.AccessPoints()` / `system.Device(ied, ap)` | 发现接入点，在有 Server 的 AP 上实例化设备模型 |
| `ied.Points()` / `ied.Resolve(ref, fc)` / `ied.Lookup(ld, ln, fc, path)` | 浏览或解析经模型校验的 MMS 对象名 |
| `ied.Signal(ld, ln, fc, object, member)` / `signal.Sample(value, q, t)` | 一次绑定同一信号的值、品质、时标，并构造整批采集更新 |
| `ied.Type(ctx, name)` | 查询声明的 MMS 类型及结构成员 |
| `ied.DataSets()` / `ied.DataSet(ld, ln, set)` | 获取静态列表名和有序 FCDA，用于 `ReadNamedList` |
| `ied.Streams()` / `ied.Stream(ld, cb)` / `stream.Info()` | 查看控制块引用、MAC、VLAN、重传时间和成员顺序 |
| `system.GOOSEInputs(receiver, ap)` | 枚举接收方的 intAddr、源 IED、控制块和数据路径，支持纯客户端 AP |
| `ied.Input(receiver, ap, intAddr)` | 将明确配置的接收端 ExtRef 绑定到发布数据项 |
| `ied.NewServer(cfg)` / `ied.OnOperate(name, handler)` / `ied.Update(ctx, changes...)` | 提供 MMS 服务、注册二进制控制、提交采集反馈 |

当前支持所选接入点的逻辑设备、带前缀的 LN、非数组 DO/SDO/DA/BDA 模板、嵌套结构、枚举和实例初值。支持明确指定目标的 FCDA（整 DO 或属性）、普通二层 GOOSE 静态配置、六字段 Oper 的 SPC/DPC 普通安全直接控制。数组、定值组、SBO/增强控制、RCB 报告、在线 GoCB 重配置超出该实现范围。一次接线拓扑属于描述信息。解析输入限制为 16 MiB，会检查实际参与映射的引用。枚举按有符号 8 位值处理，未给初值的枚举从 0 开始。完整模式校验应使用与 SCD 版次匹配的 XSD。

示例文件已通过 IEC 61850-6 SCL 2007B4 XSD 校验。SCL 对应 [IEC 61850-6 第 2.1 版](https://webstore.iec.ch/en/publication/63319)，MMS/GOOSE 映射对应 [IEC 61850-8-1](https://webstore.iec.ch/en/publication/66585)。可对照 libiec61850 的[客户端控制接口](https://support.mz-automation.de/doc/libiec61850/c/latest/group__IEC61850__CLIENT__CONTROL.html)。
