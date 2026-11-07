# SV API：发布、接收和解析采样值

[English](sv.md) | 简体中文

SV的`ASDU.Data`是应用约定的字节布局，没有MMS类型标签。两端要约定svID、数据集、修订、采样计数模数和每项的偏移/类型。

直接运行 [publish-subscribe](../../examples/sv/publish-subscribe/main.go) 与 [data-layout](../../examples/sv/data-layout/main.go)，两者都不打开真实网卡。

本页函数片段使用标准库context、fmt、time，以及`github.com/mababaNiubi/go-iec61850/sv`和同模块的ethernet包。

## 编码常用 INT32/Quality 数据

```go
func EncodeMeasurements(first, second int32) ([]byte, error) {
    return sv.EncodeInt32Quality([]sv.Int32Quality{
        {Value: first, Quality: 0},
        {Value: second, Quality: 0x0400},
    })
}

func DecodeMeasurements(asdu sv.ASDU) error {
    values, err := sv.DecodeInt32Quality(asdu.Data)
    if err != nil { return err } // 长度必须是8的倍数。
    for i, value := range values {
        fmt.Printf("sample[%d]=%d quality=%#x\n", i, value.Value, value.Quality)
    }
    return nil
}
```

每组占8字节：4字节有符号INT32，再跟4字节Quality，均为网络字节序。这个辅助函数不推断比例、单位或SCL布局；Quality保留原始位。

## 创建发布者，更新下一次采样

这里复用上节EncodeMeasurements函数，保持两端布局一致。

```go
func NewSamplePublisher(link sv.FrameIO, source [6]byte) (*sv.Publisher, error) {
    raw, err := EncodeMeasurements(100, -100)
    if err != nil { return nil, err }
    frame := sv.Frame{
        Destination: [6]byte{1, 0x0c, 0xcd, 4, 0, 1}, Source: source, APPID: 0x4000,
        PDU: sv.PDU{ASDUs: []sv.ASDU{{
            SVID: "sampleSV", DataSet: "sample/LLN0$sv", ConfRev: 1,
            SampleCount: 0, SampleSync: 0, Data: raw,
        }}},
    }
    return sv.NewPublisher(link, sv.PublisherConfig{
        Frame: frame, Interval: time.Millisecond, CounterModulus: 4000,
    })
}

func UpdateMeasurements(publisher *sv.Publisher, first, second int32) error {
    raw, err := EncodeMeasurements(first, second)
    if err != nil { return err }
    return publisher.UpdateData(0, raw) // 0是本帧第一个ASDU的索引。
}
```

| 配置/调用 | 含义 |
|---|---|
| `SVID` / `DataSet` / `ConfRev` | 流标识、数据集引用、配置修订；与订阅端约定 |
| `SampleCount` | 保留初始计数，成功发送后自动推进 |
| `SampleSync=0` | 未同步；不能因设置为1/2就认定已经有同步时钟 |
| `Interval` | 软件帧发送间隔；不等于自动采集硬件，也不自动填写SampleRate |
| `CounterModulus` | 0选65536；可配置2..65536，如4000；两端必须一致 |
| `UpdateData(index,raw)` | 复制新数据，长度必须与初始ASDU相同 |
| `Publish(ctx)` / `Run(ctx)` | 单次发送 / 周期发送，二者不并发调用 |

Run运行时可从业务goroutine调用UpdateData。采样率、布局或修订改变时建立新Publisher。迟到的发送槽会被跳过并推进计数，SkippedSlots记录跳过数量，不补发突发采样。

## 订阅并解析每个匹配 ASDU

```go
func NewSampleSubscriber() (*sv.Subscriber, error) {
    return sv.NewSubscriber(sv.SubscriptionConfig{
        Destination: [6]byte{1, 0x0c, 0xcd, 4, 0, 1}, APPID: 0x4000,
        SVID: "sampleSV", DataSet: "sample/LLN0$sv", ConfRev: 1,
        DataLength: 16, CounterModulus: 4000, Timeout: time.Second,
        AllowUnsynchronized: true, // 只因这里明确接收软件演示的SampleSync0。
    })
}

func AcceptSamples(subscriber *sv.Subscriber, packet []byte) error {
    frame, err := sv.DecodeFrame(packet)
    if err != nil { return err }
    events, err := subscriber.Process(frame)
    if err != nil { return err }
    for _, event := range events {
        fmt.Printf("%s smpCnt=%d missing=%d\n", event.Kind, event.ASDU.SampleCount, event.MissingSamples)
        if err := DecodeMeasurements(event.ASDU); err != nil { return err }
    }
    return nil
}
```

`Process`返回事件切片，因为一帧可包含多个匹配ASDU。匹配项全部校验后才提交，后部无效采样不会部分推进状态。`DataLength=0`固定首次有效长度，`ConfRev=0`固定首次修订。

SV没有线上TAL。Timeout是本地策略，默认1秒。库默认拒绝SampleSync0；只有应用明确接受未同步流时才设置AllowUnsynchronized。

## 持续收包和丢样检测

下面复用上节NewSampleSubscriber；用Run驱动收包和本地超时。

```go
func ListenSamples(ctx context.Context, link sv.FrameIO) error {
    subscriber, err := NewSampleSubscriber()
    if err != nil { return err }
    show := func(event sv.Event) {
        switch event.Kind {
        case sv.Expired:
            fmt.Println("本地接收超时")
        case sv.SampleGap:
            fmt.Printf("丢样%d，当前计数%d\n", event.MissingSamples, event.ASDU.SampleCount)
        case sv.Recovered:
            fmt.Println("采样流恢复")
        default:
            fmt.Println("smpCnt:", event.ASDU.SampleCount)
        }
    }
    done := make(chan error, 1)
    go func() { done <- subscriber.Run(ctx, link) }()
    for {
        select {
        case event := <-subscriber.Events(): show(event)
        case err := <-done:
            for {
                select {
                case event := <-subscriber.Events(): show(event)
                default: return err
                }
            }
        }
    }
}
```

重复/旧计数不刷新超时；前进跳号生成SampleGap。Snapshot返回最后一个匹配ASDU及有效性，Stats记录累计丢样等诊断。Events不关闭，要同时观察Run返回结果；队列满返回ErrEventOverflow。

## 一帧装多个 ASDU

```go
func TwoSamples(first, second []byte) sv.PDU {
    return sv.PDU{ASDUs: []sv.ASDU{
        {SVID: "sampleSV", DataSet: "sample/LLN0$sv", ConfRev: 1, SampleCount: 0, SampleSync: 0, Data: first},
        {SVID: "sampleSV", DataSet: "sample/LLN0$sv", ConfRev: 1, SampleCount: 1, SampleSync: 0, Data: second},
    }}
}
```

同一svID在帧内计数必须连续：0/1发送后，下一帧为2/3。重复同一svID必须共享数据集、修订、长度、速率/模式；不同svID各自推进。上面函数返回PDU，还需放入有合法MAC和APPID的Frame，再创建Publisher。切片由调用方管理；Frame/ASDU.Clone可取得副本。

## 自己定义采样值布局

```go
func EncodeCustomSample(current int32, quality uint32, frequency float32) ([]byte, error) {
    raw, err := sv.NewSampleData(12)
    if err != nil { return nil, err }
    if err := raw.SetInt32(0, current); err != nil { return nil, err }
    if err := raw.SetQuality(4, quality); err != nil { return nil, err }
    if err := raw.SetFloat32(8, frequency); err != nil { return nil, err }
    return raw.Bytes(), nil
}
```

读取时把Data转换为`sv.SampleData(asdu.Data)`后使用同样偏移：Int32(0)、Quality(4)、Float32(8)。访问器均返回错误检查越界；Bytes返回副本。不要用Go struct的内存大小猜线上布局，因为对齐可能插入填充。完整例子：[data-layout](../../examples/sv/data-layout/main.go)。

## 真实网卡与可选元数据

```go
func PublishSamplesOnInterface(ctx context.Context, interfaceName string) error {
    conn, err := ethernet.Open(ethernet.Config{Interface: interfaceName, EtherType: sv.EtherType})
    if err != nil { return err }
    defer conn.Close()
    publisher, err := NewSamplePublisher(conn, conn.MAC())
    if err != nil { return err }
    return publisher.Run(ctx)
}
```

可选字段用指针区分缺省与显式0，例如SampleRate、SampleMode、RefreshTime、GMIdentity。当前SampleMode采用两字节参考publisher profile；固定C版本自身subscriber只接受一字节，相关不对称对照记录在本地资料中。这不是已完成所有标准profile验证的声明。

本库提供普通 L2 SV，可通过 `scl.SVStream` 载入 SCL 布局与流配置；不提供 PTP、硬件采集、在线 SVCB 预留或 R-SV。接口组播权限与VLAN保真见 [平台说明](../platforms.zh-CN.md)。

## 设备级接入

[公开设备 API](ied.zh-CN.md) 包含按 ctlModel 控制、URCB 报告、嵌入模拟器、结构字段路径、SCL 定义的 SV 布局和 GOOSE/SV 多流接收方法。
