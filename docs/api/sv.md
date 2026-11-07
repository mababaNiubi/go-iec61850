# SV API: publish, receive, and decode sampled values

English | [简体中文](sv.zh-CN.md)

`ASDU.Data` contains an application-defined byte layout without MMS type tags. Both endpoints must agree on svID, dataset, configuration revision, counter modulus, and each field's type and offset.

The [publish-subscribe](../../examples/sv/publish-subscribe/main.go) and [data-layout](../../examples/sv/data-layout/main.go) examples run without opening a network interface.

These snippets use the standard `context`, `fmt`, and `time` packages, `github.com/mababaNiubi/go-iec61850/sv`, and the module's `ethernet` package.

## Encode an INT32/Quality layout

```go
func EncodeMeasurements(first, second int32) ([]byte, error) {
    return sv.EncodeInt32Quality([]sv.Int32Quality{
        {Value: first, Quality: 0},
        {Value: second, Quality: 0x0400},
    })
}

func DecodeMeasurements(asdu sv.ASDU) error {
    values, err := sv.DecodeInt32Quality(asdu.Data)
    if err != nil { return err } // The payload length must be a multiple of eight.
    for i, value := range values {
        fmt.Printf("sample[%d]=%d quality=%#x\n", i, value.Value, value.Quality)
    }
    return nil
}
```

Each pair occupies eight bytes: a signed 4-byte INT32 followed by a 4-byte Quality, both in network byte order. The helpers preserve raw quality bits and do not infer scaling, units, or an SCL layout.

## Create a publisher and update the next sample

This example reuses `EncodeMeasurements` above so both endpoints use the same layout.

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
    return publisher.UpdateData(0, raw) // Zero selects the first ASDU in this frame.
}
```

| Setting or call | Meaning |
|---|---|
| `SVID` / `DataSet` / `ConfRev` | Stream identifier, dataset reference, and configuration revision agreed with the subscriber |
| `SampleCount` | Initial counter; it advances automatically after successful sends |
| `SampleSync: 0` | Unsynchronized. Setting it to 1 or 2 does not create a synchronized clock |
| `Interval` | Software interval between frames; it does not acquire hardware samples or populate SampleRate |
| `CounterModulus` | Zero selects 65536; the configurable range is 2..65536, for example 4000. Both endpoints must agree |
| `UpdateData(index, raw)` | Copy replacement data; its length must match the original ASDU |
| `Publish(ctx)` / `Run(ctx)` | Send once / run periodically; do not call them concurrently |

While `Run` is active, a business goroutine may call `UpdateData`. Create a new publisher when changing rate metadata, layout, or revision. Late send slots are skipped and counters advance accordingly; `SkippedSlots` records the skipped count. The runtime does not send a burst to catch up.

## Subscribe and decode each matching ASDU

```go
func NewSampleSubscriber() (*sv.Subscriber, error) {
    return sv.NewSubscriber(sv.SubscriptionConfig{
        Destination: [6]byte{1, 0x0c, 0xcd, 4, 0, 1}, APPID: 0x4000,
        SVID: "sampleSV", DataSet: "sample/LLN0$sv", ConfRev: 1,
        DataLength: 16, CounterModulus: 4000, Timeout: time.Second,
        AllowUnsynchronized: true, // Explicitly accept SampleSync 0 for this software demonstration.
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

`Process` returns a slice because one frame may contain multiple matching ASDUs. It validates all matching entries before committing state, so a later invalid sample cannot partially advance the stream. `DataLength: 0` pins the first valid payload length; `ConfRev: 0` pins the first accepted revision.

SV carries no TAL field. `Timeout` is a local reception policy and defaults to one second. The library rejects SampleSync 0 by default; set `AllowUnsynchronized` only when the application explicitly accepts an unsynchronized stream.

## Receive continuously and detect missing samples

Reuse `NewSampleSubscriber` above and let `Run` handle reception and local timeout checks:

```go
func ListenSamples(ctx context.Context, link sv.FrameIO) error {
    subscriber, err := NewSampleSubscriber()
    if err != nil { return err }
    show := func(event sv.Event) {
        switch event.Kind {
        case sv.Expired:
            fmt.Println("Local receive timeout")
        case sv.SampleGap:
            fmt.Printf("missing %d samples; current count %d\n", event.MissingSamples, event.ASDU.SampleCount)
        case sv.Recovered:
            fmt.Println("Sample stream recovered")
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

Duplicate or old counts do not refresh the timeout; forward gaps generate `SampleGap`. `Snapshot` returns the last matching ASDU and validity, while `Stats` records cumulative missing samples and diagnostics. `Events` is not closed; also observe the return from `Run`. A full queue returns `ErrEventOverflow`.

## Put multiple ASDUs in one frame

```go
func TwoSamples(first, second []byte) sv.PDU {
    return sv.PDU{ASDUs: []sv.ASDU{
        {SVID: "sampleSV", DataSet: "sample/LLN0$sv", ConfRev: 1, SampleCount: 0, SampleSync: 0, Data: first},
        {SVID: "sampleSV", DataSet: "sample/LLN0$sv", ConfRev: 1, SampleCount: 1, SampleSync: 0, Data: second},
    }}
}
```

Counters for a repeated svID must be consecutive within a frame: after 0/1, the next frame carries 2/3. Entries sharing an svID must also share dataset, revision, length, and rate/mode metadata; different svIDs advance independently. Put this PDU in a frame with valid MAC addresses and APPID before creating a publisher. The caller owns the slices; use `Frame.Clone` or `ASDU.Clone` for a defensive copy.

## Define a custom sample layout

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

For reading, convert the payload to `sv.SampleData(asdu.Data)` and use matching offsets: `Int32(0)`, `Quality(4)`, and `Float32(8)`. Accessors return errors for out-of-bounds accesses; `Bytes()` returns a copy. Use explicit wire offsets because a Go struct may contain alignment padding. See the full [data-layout example](../../examples/sv/data-layout/main.go).

## Use a real interface and optional metadata

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

Optional fields use pointers to distinguish absence from an explicit zero, including SampleRate, SampleMode, RefreshTime, and GMIdentity. The current SampleMode representation follows the two-byte reference publisher profile. The pinned C reference subscriber accepts only one byte; the resulting asymmetry is recorded in the local interoperability material. Support has not been validated against every standard profile.

The package implements ordinary Layer 2 SV. Typed layouts and profiles can be loaded through `scl.SVStream`. It does not supply PTP, hardware acquisition, online SVCB reservation, or R-SV. See [platform support](../platforms.md) for multicast access and VLAN fidelity.

## Device-level integration

See the [public device API](ied.md) for ctlModel-based controls, URCB reports, embedded simulation, typed component paths, SCL-defined SV layouts and shared GOOSE/SV receivers.
