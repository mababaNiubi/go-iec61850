# GOOSE API：发布和订阅状态量

[English](goose.md) | 简体中文

GOOSE传输完整Ethernet帧，不连接TCP地址。业务数据使用`data.Value`，发布对象负责事件时间、stNum、sqNum和重传；接收对象负责过滤、重复判断和TAL失效。

先运行不需要网卡的 [publish-subscribe示例](../../examples/goose/publish-subscribe/main.go)：`go run ./examples/goose/publish-subscribe`。

本页函数片段使用标准库context、fmt、time，以及模块中的goose、data、ethernet包；具体导入路径前缀为`github.com/mababaNiubi/go-iec61850/`。

## 创建发布者并发送状态

```go
func NewEventPublisher(link goose.FrameIO, source [6]byte) (*goose.Publisher, error) {
    frame := goose.Frame{
        Destination: [6]byte{1, 0x0c, 0xcd, 1, 0, 1}, Source: source,
        APPID: 0x1000, VLAN: &goose.VLAN{Priority: 4, ID: 123},
        PDU: goose.PDU{
            GoCBRef: "sample/LLN0$GO$events", DataSet: "sample/LLN0$events", ConfRev: 1,
            Values: []data.Value{data.Boolean(false)},
        },
    }
    return goose.NewPublisher(link, goose.PublisherConfig{
        Frame: frame, MinTime: 4*time.Millisecond, MaxTime: time.Second,
        Types: []data.TypeSpec{{Kind: data.KindBoolean}},
    })
}
```

| 配置 | 你需要填写什么 |
|---|---|
| `link` | FrameIO；真实接口用`ethernet.Open`返回的Conn，内存示例自行实现 |
| `Source` | 真实网卡用`conn.MAC()`；必须是非零单播地址 |
| `Destination` / `APPID` | 与接收端约定的组播MAC和应用标识 |
| `GoCBRef` / `DataSet` / `ConfRev` | 控制块引用、数据集引用、配置修订号；两端一致 |
| `Values` / `Types` | 按数据集顺序提供值，可用Types约束每项声明类型 |
| `VLAN` | nil表示无标签，否则填写PCP/DEI/VID |
| `MinTime` / `MaxTime` | 重传从初始间隔增加到稳定间隔；默认4ms和1s |

业务变化时调用Update，不要手动维护计数器：

```go
func SetActive(ctx context.Context, publisher *goose.Publisher, active bool) error {
    changed, err := publisher.Update([]data.Value{data.Boolean(active)})
    if err != nil { return err }
    fmt.Println("业务值是否改变:", changed)
    return publisher.Publish(ctx) // 单次发送；Run正在运行时不并发调用Publish。
}
```

`Update`返回`(changed bool,error)`。值相同不增加stNum，值变化时增加stNum、sqNum归0。生产应用通常调用`publisher.Run(ctx)`持续发布，并从业务goroutine调用Update；Run会被更新唤醒并负责重传。仅调用Publish时，应用必须自行在TAL到期前安排后续发送。

## 创建订阅者并处理收到的帧

```go
func NewEventSubscriber() (*goose.Subscriber, error) {
    vlan := uint16(123)
    return goose.NewSubscriber(goose.SubscriptionConfig{
        Destination: [6]byte{1, 0x0c, 0xcd, 1, 0, 1}, APPID: 0x1000, VLANID: &vlan,
        GoCBRef: "sample/LLN0$GO$events", DataSet: "sample/LLN0$events", ConfRev: 1,
        Types: []data.TypeSpec{{Kind: data.KindBoolean}}, // 验证项数与类型后再读取Values[0]。
    })
}

func AcceptEvent(subscriber *goose.Subscriber, packet []byte) error {
    frame, err := goose.DecodeFrame(packet)
    if err != nil { return err }
    event, err := subscriber.Process(frame)
    if err != nil { return err } // 不匹配、重复或错误帧都不会覆盖有效业务值。
    active, err := event.Frame.PDU.Values[0].Bool()
    if err != nil { return err }
    fmt.Printf("%s: active=%t st=%d sq=%d\n", event.Kind, active, frame.PDU.StateNumber, frame.PDU.SequenceNumber)
    return nil
}
```

`VLANID=nil`接受有/无标签；`RequireUntagged=true`限定无标签。`ConfRev=0`固定首次接受的修订，而非允许任意后续修订。可额外限定Source、GoID和Types。仿真与needsCommission默认拒绝，需要显式放宽策略。

## 持续接收，响应失效和恢复

下面函数复用上节NewEventSubscriber，由调用方通过ctx控制运行时间。Run管理收包和TAL计时，Events返回有效事件：

```go
func ListenEvents(ctx context.Context, link goose.FrameIO) error {
    subscriber, err := NewEventSubscriber()
    if err != nil { return err }
    show := func(event goose.Event) {
        switch event.Kind {
        case goose.Expired:
            fmt.Println("TAL到期，业务值失效")
        case goose.Recovered:
            fmt.Println("收到有效报文，业务值恢复")
        default:
            fmt.Println(event.Kind, event.Frame.PDU.Values)
        }
    }
    done := make(chan error, 1)
    go func() { done <- subscriber.Run(ctx, link) }()
    for {
        select {
        case event := <-subscriber.Events():
            show(event)
        case err := <-done:
            // Run已退出，排空已经接收的事件；Events本身不会关闭。
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

- `StateChanged`：首次或新业务状态；`Retransmission`：同一状态的较新重传序号。
- `Expired`：TAL到期；`Recovered`：失效后重新收到有效数据。
- `Snapshot()`返回副本和当前有效性；外部收包循环可调用`CheckTimeout()`推进失效检查。
- Events队列满时Run返回`ErrEventOverflow`。不匹配/畸形输入计入Stats/LastError，Run继续收包。
- 链路由调用方关闭；上面的函数只有收到Run结果才返回，不遗留收包goroutine。

## 换成真实网卡

```go
func PublishOnInterface(ctx context.Context, interfaceName string) error {
    conn, err := ethernet.Open(ethernet.Config{Interface: interfaceName, EtherType: goose.EtherType})
    if err != nil { return err }
    defer conn.Close()
    publisher, err := NewEventPublisher(conn, conn.MAC())
    if err != nil { return err }
    return publisher.Run(ctx)
}
```

订阅也把Conn传给ListenEvents。Windows接口不投递目标组播时，显式配置`Promiscuous:true`；Linux需要raw socket权限。已有工具见 [cmd](../../cmd/README.zh-CN.md)。内核/驱动的VLAN保真与软件调度边界见 [平台说明](../platforms.zh-CN.md)。

普通L2 GOOSE不包含GoCB/SCL自动配置、R-GOOSE或安全认证。重启恢复需显式AllowRestartAfterTimeout；它不提供防重放认证能力。
