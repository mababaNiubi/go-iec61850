# GOOSE API: publish and subscribe to state changes

English | [简体中文](goose.zh-CN.md)

GOOSE transports complete Ethernet frames. Application data uses `data.Value`. A publisher manages event time, stNum, sqNum, and retransmission; a subscriber handles filtering, duplicate detection, and time-allowed-to-live (TAL) expiry.

Start with the [publish-subscribe example](../../examples/goose/publish-subscribe/main.go): `go run ./examples/goose/publish-subscribe`. It requires no network interface.

The snippets use the standard `context`, `fmt`, and `time` packages, plus `goose`, `data`, and `ethernet` from `github.com/mababaNiubi/go-iec61850/`.

## Create a publisher and send a state

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

| Setting | What to supply |
|---|---|
| `link` | A `FrameIO`; use the `Conn` returned by `ethernet.Open` for a real interface, or an in-memory implementation |
| `Source` | Use `conn.MAC()` on a real interface; it must be a nonzero unicast address |
| `Destination` / `APPID` | The multicast MAC and application identifier agreed with the receiver |
| `GoCBRef` / `DataSet` / `ConfRev` | Control block reference, dataset reference, and configuration revision, matching the receiver |
| `Values` / `Types` | Values in dataset order; optional `Types` constrain each declared type |
| `VLAN` | Nil for untagged frames; otherwise specify PCP, DEI, and VID |
| `MinTime` / `MaxTime` | Retransmission grows from the initial to the steady interval; defaults are 4 ms and 1 second |

Call `Update` when application data changes; the publisher maintains the counters:

```go
func SetActive(ctx context.Context, publisher *goose.Publisher, active bool) error {
    changed, err := publisher.Update([]data.Value{data.Boolean(active)})
    if err != nil { return err }
    fmt.Println("value changed:", changed)
    return publisher.Publish(ctx) // Send once; do not call Publish concurrently with Run.
}
```

`Update` returns `(changed bool, error)`. An unchanged value leaves stNum unchanged. A changed value advances stNum and resets sqNum to zero. Applications normally call `publisher.Run(ctx)` continuously and call `Update` from another goroutine; the update wakes the publisher and `Run` schedules retransmissions. When using only `Publish`, the application must arrange further sends before TAL expires.

## Create a subscriber and process a frame

```go
func NewEventSubscriber() (*goose.Subscriber, error) {
    vlan := uint16(123)
    return goose.NewSubscriber(goose.SubscriptionConfig{
        Destination: [6]byte{1, 0x0c, 0xcd, 1, 0, 1}, APPID: 0x1000, VLANID: &vlan,
        GoCBRef: "sample/LLN0$GO$events", DataSet: "sample/LLN0$events", ConfRev: 1,
        Types: []data.TypeSpec{{Kind: data.KindBoolean}}, // Validate the number and types of entries before indexing Values[0].
    })
}

func AcceptEvent(subscriber *goose.Subscriber, packet []byte) error {
    frame, err := goose.DecodeFrame(packet)
    if err != nil { return err }
    event, err := subscriber.Process(frame)
    if err != nil { return err } // Unmatched, duplicate or invalid frames do not replace valid application data.
    active, err := event.Frame.PDU.Values[0].Bool()
    if err != nil { return err }
    fmt.Printf("%s: active=%t st=%d sq=%d\n", event.Kind, active, frame.PDU.StateNumber, frame.PDU.SequenceNumber)
    return nil
}
```

`VLANID: nil` accepts tagged and untagged input; `RequireUntagged: true` restricts it to untagged frames. `ConfRev: 0` pins the first accepted revision. Optional filters also cover source MAC, GoID, and types. Simulation and needsCommission frames are rejected by default; accepting them requires explicit configuration.

## Receive continuously and handle expiry or recovery

This function reuses `NewEventSubscriber` above. The caller controls its lifetime through `ctx`. `Run` receives frames and manages TAL timers; `Events` delivers accepted events:

```go
func ListenEvents(ctx context.Context, link goose.FrameIO) error {
    subscriber, err := NewEventSubscriber()
    if err != nil { return err }
    show := func(event goose.Event) {
        switch event.Kind {
        case goose.Expired:
            fmt.Println("TAL expired; application data is no longer valid")
        case goose.Recovered:
            fmt.Println("Valid frame received; application data recovered")
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
            // Run has stopped. Drain queued events; Events itself is not closed.
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

- `StateChanged`: the first accepted state or a new application state. `Retransmission`: a newer retransmission sequence for the same state.
- `Expired`: TAL elapsed. `Recovered`: valid data arrived after expiry.
- `Snapshot()` returns a copy and its validity. An external receive loop can use `CheckTimeout()` to advance expiry checks.
- A full event queue makes `Run` return `ErrEventOverflow`. Unmatched or malformed input is recorded in `Stats`/`LastError` while reception continues.
- The caller owns and closes the link. The example waits for `Run` to return before exiting.

## Use a real Ethernet interface

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

For reception, pass the same kind of connection to `ListenEvents`. If a Windows interface does not deliver the target multicast group, explicitly set `Promiscuous: true`. Linux requires raw socket permissions. See the [command-line tools](../../cmd/README.md) and [platform notes](../platforms.md) for driver behavior, VLAN fidelity, and software scheduling limits.

For SCD-defined GSEControl, Communication/GSE and subscriber ExtRef, use the [SCD integration API](scl.md). The `goose` package implements ordinary Layer 2 GOOSE. Restart recovery is opt-in through `AllowRestartAfterTimeout`; that policy does not provide authenticated replay protection.
