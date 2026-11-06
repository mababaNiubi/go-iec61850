# MMS API: common application tasks

English | [简体中文](mms.zh-CN.md)

Import `github.com/mababaNiubi/go-iec61850/mms`. The first snippet is a complete program. Subsequent client functions reuse an established `*mms.Client`; each request takes a context that bounds its wait. The server snippet also imports the standard `net` package, and the error-handling snippet imports `errors`.

## Connect and read or write an integer

Start the sample peer with `go run ./cmd/mms-server -address 127.0.0.1:8102`. Save the following program in your application, or run the [read-write example](../../examples/mms/read-write/main.go).

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/mababaNiubi/go-iec61850/mms"
)

func main() {
    if err := run(); err != nil {
        log.Fatal(err) // Deferred cleanup inside run has completed before logging the error.
    }
}

func run() error {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    client, err := mms.Dial(ctx, "127.0.0.1:8102", mms.Config{})
    if err != nil { return err }
    defer client.Close() // Close the connection on every error path.

    name := mms.DomainName("sample", "int")
    if err := client.Write(ctx, name, mms.Integer(99)); err != nil { return err }
    value, err := client.Read(ctx, name)
    if err != nil { return err }

    number, err := value.Int() // Convert an MMS signed integer to a Go int64.
    if err != nil { return err }
    fmt.Println("sample/int =", number)

    return client.Conclude(ctx) // Release the MMS association after all requests finish.
}
```

| Call or parameter | Meaning |
|---|---|
| `mms.Dial(ctx, address, config)` | Establish TCP, ISO, and MMS associations; return a reusable client or an error |
| `address` | Peer TCP address in `host:port` form. The example uses 8102; use the device's listening port |
| `mms.Config{}` | Defaults: maximum PDU size 65000, request timeout 5 seconds, and up to 5 outstanding calls; association limits are negotiated with the peer |
| `DomainName("sample", "int")` | `sample` is the peer's domain and `int` is its item name; `sample/int` is only a display format |
| `mms.Integer(99)` | Construct a signed integer value. It does not declare a 32/64-bit width; the device's type controls that constraint. Use `ValidateValue` for a local check |
| `Read` / `Write` | Return `(Value, error)` / `error` |
| `Conclude` / `Close` | Gracefully release the association after outstanding calls finish / abort the connection, also suitable for deferred cleanup |

Use names from the device configuration or directory. If an item contains `$`, pass it unchanged as the second argument of `DomainName`. Keep the domain and item as separate arguments.

## Configure timeouts and device selectors

Start with a zero-value `Config`. Specify ISO fields when required by the device:

```go
func DialDevice(ctx context.Context, address string, remoteTSelector []byte) (*mms.Client, error) {
    return mms.Dial(ctx, address, mms.Config{
        RequestTimeout: 2*time.Second, // Read/write timeout; ctx and ISO.HandshakeTimeout govern connection setup.
        MaxPDUSize: 65000,
        ISO: mms.ISOConfig{RemoteTSelector: remoteTSelector},
    })
}
```

Selectors are byte sequences. A nil selector uses the library default; a non-nil empty slice explicitly selects an empty value. Supply local/remote T, S, and P selectors and AP titles from the device configuration. `Dial`'s context governs setup and does not bind the entire lifetime of the returned client; close or conclude it explicitly.

## Read several variables

Batch operations have two error levels: the returned `err` describes the request as a whole, and `results[i].Error` describes one variable. Results retain access-list order.

```go
func ReadMany(ctx context.Context, client *mms.Client) error {
    accesses := []mms.VariableAccess{
        mms.Access(mms.DomainName("sample", "int")),
        mms.Access(mms.DomainName("sample", "bool")),
        mms.Access(mms.DomainName("sample", "missing")),
    }
    results, err := client.ReadMultiple(ctx, accesses...)
    if err != nil { return err }
    for i, result := range results {
        if result.Error != nil {
            fmt.Printf("%s read failed: %v\n", accesses[i].Name, result.Error)
            continue
        }
        fmt.Printf("%s = %s (type=%s)\n", accesses[i].Name, result.Value, result.Value.Kind())
    }
    return nil
}
```

`WriteMultiple(ctx, accesses, values)` similarly returns `([]error, error)`. A nil entry indicates that item's write succeeded. Writes can partially succeed; there is no transactional rollback. See the runnable [read-batch example](../../examples/mms/read-batch/main.go).

## Read an array element or structure member

```go
func ReadParts(ctx context.Context, client *mms.Client) error {
    // Indices start at zero; read the second array element.
    item, err := client.ReadAccess(ctx, mms.Element(mms.DomainName("sample", "array"), 1))
    if err != nil { return err }
    number, err := item.Int()
    if err != nil { return err }

    member, err := client.ReadAccess(ctx, mms.Member(mms.DomainName("sample", "struct"), "count"))
    if err != nil { return err }
    count, err := member.Int()
    if err != nil { return err }
    fmt.Printf("array[1]=%d, struct.count=%d\n", number, count)
    return nil
}
```

Use `WriteAccess(ctx, access, value)` to write the selected location. `Element` and `Member` represent single-level AlternateAccess. Handle access errors from devices that do not support it. To read the whole array or structure, use `Read(ctx, name)` and then `Value.Elements()`.

## Discover domains, variable names, and types

```go
func Browse(ctx context.Context, client *mms.Client) error {
    domains, err := client.Names(ctx, mms.DomainObject, mms.VMDScope, "")
    if err != nil { return err }
    fmt.Println("domains:", domains)

    names, err := client.Names(ctx, mms.NamedVariable, mms.DomainScope, "sample")
    if err != nil { return err }
    fmt.Println("variables:", names)

    spec, err := client.GetVariableAccessAttributes(ctx, mms.DomainName("sample", "int"))
    if err != nil { return err }
    fmt.Printf("type=%s width=%d\n", spec.Kind, spec.Width)
    return nil
}
```

`Names` collects all pages automatically. For manual pagination, call `GetNameList(ctx, class, scope, domain, after)` with the preceding page's last name as `after`, until `MoreFollows` is false. The returned type specification describes declared widths, array lengths, and structure members. Infer constraints from that specification rather than from a current value. See [browse](../../examples/mms/browse/main.go).

## Read and write a named variable list

This example defines an association-specific (AA) list, which disappears when the association closes. Dynamic MMS lists are separate from SCL datasets and report control blocks. A device may reject dynamic definitions.

```go
func ReadWriteList(ctx context.Context, client *mms.Client) error {
    name := mms.AssociationName("watch")
    variables := []mms.VariableAccess{
        mms.Access(mms.DomainName("sample", "int")),
        mms.Access(mms.DomainName("sample", "bool")),
    }
    if err := client.DefineNamedList(ctx, name, variables); err != nil { return err }

    failures, err := client.WriteNamedList(ctx, name, []mms.Value{mms.Integer(100), mms.Boolean(false)})
    if err != nil { return err }
    for i, failure := range failures {
        if failure != nil { return fmt.Errorf("write list item %d: %w", i, failure) }
    }
    results, err := client.ReadNamedList(ctx, name)
    if err != nil { return err }
    for i, result := range results {
        if result.Error != nil { return result.Error }
        fmt.Printf("watch[%d] = %s\n", i, result.Value)
    }
    _, err = client.DeleteNamedList(ctx, name)
    return err
}
```

For an existing list, call `ReadNamedList` directly. `GetNamedListAttributes` returns its members and `Deletable` flag. `DeleteResult` distinguishes matched and deleted list counts. See [named-list](../../examples/mms/named-list/main.go).

## Receive and send InformationReport

A client receives raw MMS reports through `Reports()`. This function waits for one report or cancellation:

```go
func WaitReport(ctx context.Context, client *mms.Client) (mms.InformationReport, error) {
    select {
    case report, open := <-client.Reports():
        if !open { return mms.InformationReport{}, client.Err() }
        return report, nil
    case <-ctx.Done():
        return mms.InformationReport{}, ctx.Err()
    }
}
```

Check each `report.Results[i].Error` before reading its value. An explicit-variable report carries `Variables`. A named-list report carries `List`; interpret its results using the known member order, since `Variables` can be empty. The library owns the receive-only channel. A full report queue closes the association with `ErrReportOverflow`.

On the server, obtain the per-connection `Association` through `Server.OnAssociation` and use it to send a report to that client:

```go
func SendMeasurement(ctx context.Context, association *mms.Association, value int64) error {
    report := mms.InformationReport{
        Variables: []mms.VariableAccess{mms.Access(mms.DomainName("sample", "int"))},
        Results: []mms.ReadResult{{Value: mms.Integer(value)}},
    }
    return association.SendReport(ctx, report)
}
```

Set the callback before `Serve` and use `association.Context()` to bound background work to the association lifetime. This API does not automatically monitor device values or configure BRCB/URCB. The runnable [report example](../../examples/mms/report/main.go) includes both endpoints.

## Create an MMS server

Register variables in a `Model`, or implement `Store` to connect reads and writes to your application. The following function serves until its context is canceled, then shuts down and waits for serving to finish:

```go
func ServeDevice(ctx context.Context, address string) error {
    model := mms.NewModel()
    if err := model.Add(mms.Variable{
        Name: mms.DomainName("sample", "int"),
        Type: mms.TypeSpec{Kind: mms.KindInteger, Width: 32},
        Value: mms.Integer(42), Writable: true,
    }); err != nil { return err }
    server, err := mms.NewServer(model, mms.Config{})
    if err != nil { return err }
    defer server.Close()
    listener, err := net.Listen("tcp", address)
    if err != nil { return err }
    defer listener.Close()

    done := make(chan error, 1)
    go func() { done <- server.Serve(listener) }()
    select {
    case err := <-done:
        return err
    case <-ctx.Done():
        cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
        defer cancel()
        err := server.Shutdown(cleanup)
        <-done
        return err
    }
}
```

`Writable` defaults to false. `Model` is safe for concurrent access and validates writes against `TypeSpec`. A custom `Store` must also support concurrent calls and honor contexts. See [mmsdemo/server.go](../../examples/internal/mmsdemo/server.go) for a loopback peer using an ephemeral port.

## Handle errors, deadlines, and concurrent calls

```go
func ReadWithDeadline(parent context.Context, client *mms.Client, name mms.ObjectName) (mms.Value, error) {
    ctx, cancel := context.WithTimeout(parent, time.Second)
    defer cancel()
    value, err := client.Read(ctx, name)
    if errors.Is(err, context.DeadlineExceeded) {
        return mms.Value{}, fmt.Errorf("read %s timed out: %w", name, err)
    }
    return value, err
}
```

| Condition | Application action |
|---|---|
| `errors.Is(err, mms.ObjectNonExistent)` | Check the domain/item name or browse the directory again |
| `errors.Is(err, mms.ObjectAccessDenied)` | Handle read-only access or peer permissions; do not assume the write succeeded |
| `errors.Is(err, mms.TypeInconsistent)` | Construct a value that matches the declared type |
| `*mms.ServiceError` / `*mms.RejectError` | Use `errors.As` to inspect service class/code or the rejection reason |
| Timeout/cancellation after a request was sent, `ErrProtocol`, or disconnect | This implementation closes the association to isolate late responses. Dial again before continuing |
| Cancellation while waiting for an outstanding-call slot | No request has been sent; cancellation alone does not close the association |

A client supports concurrent reads and writes, dispatches responses by InvokeID, and bounds outstanding requests. Wait for application requests to finish before calling `Conclude`.

## Method reference

| Purpose | Methods and results |
|---|---|
| Identity/status | `Identify(ctx) (Identity, error)`, `Status(ctx) (Status, error)` |
| Read one/many | `Read(ctx, name) (Value, error)`, `ReadMultiple(ctx, accesses...) ([]ReadResult, error)` |
| Write one/many | `Write(ctx, name, value) error`, `WriteMultiple(ctx, accesses, values) ([]error, error)` |
| Partial access | `ReadAccess` / `WriteAccess` with `Element` / `Member` |
| Names and types | `Names`, `GetNameList`, `GetVariableAccessAttributes` |
| Named lists | `DefineNamedList`, `GetNamedListAttributes`, `ReadNamedList`, `WriteNamedList`, `DeleteNamedList` |
| Reports and connection state | `Reports()`, `Done()`, `Err()` |
| Graceful release/abort | `Conclude(ctx) error`, `Close() error` |

[Value conversion](data.md) · [API guide](README.md)
