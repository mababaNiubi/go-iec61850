# MMS API：按常用场景调用

[English](mms.md) | 简体中文

导入 `github.com/mababaNiubi/go-iec61850/mms`。下面第一个代码块是完整程序，后面的客户端函数可复用已经连接好的 `*mms.Client`，每个请求的 `ctx` 控制等待时间。服务端片段另使用标准库net，错误处理片段另使用errors。

## 连接设备并读写一个整型变量

先启动测试服务：`go run ./cmd/iedlab protocol mms-server -address 127.0.0.1:8102`。然后把下面代码保存为一个Go程序执行，或运行 [read-write示例](../../examples/mms/read-write/main.go)。

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
        log.Fatal(err) // run 返回后，里面的 defer 已经执行。
    }
}

func run() error {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    client, err := mms.Dial(ctx, "127.0.0.1:8102", mms.Config{})
    if err != nil { return err }
    defer client.Close() // 任何错误路径都能关闭连接。

    name := mms.DomainName("sample", "int")
    if err := client.Write(ctx, name, mms.Integer(99)); err != nil { return err }
    value, err := client.Read(ctx, name)
    if err != nil { return err }

    number, err := value.Int() // MMS有符号整数转换成Go的int64。
    if err != nil { return err }
    fmt.Println("sample/int =", number)

    return client.Conclude(ctx) // 请求结束后正常释放MMS关联。
}
```

| 调用或参数 | 具体含义 |
|---|---|
| `mms.Dial(ctx, address, config)` | 建立TCP、ISO与MMS关联；返回可复用的Client或错误 |
| `address` | 对端TCP地址，格式为host:port；示例使用8102，按设备实际监听端口修改 |
| `mms.Config{}` | 默认PDU上限65000、请求超时5秒、在途调用上限5；关联时与对端协商 |
| `DomainName("sample", "int")` | `sample`是对端Domain，`int`是域内对象名；`sample/int`只是打印形式 |
| `mms.Integer(99)` | 构造有符号整数Value；它不声明32/64位宽度，目标类型由设备约束，可用ValidateValue预检 |
| `Read` / `Write` | 读取返回`(Value,error)`；写入返回`error` |
| `Conclude` / `Close` | 前者正常释放且不能与未完成请求并发；后者中止连接并可作为defer兜底 |

对象名必须来自设备配置或目录。如果目录返回带`$`的item名，原样传给`DomainName`的第二个参数，不要把Domain和item拼成一个参数。

## 配置请求超时和设备选择子

普通连接先用Config零值，只有设备要求不同选择子时才指定ISO字段：

```go
func DialDevice(ctx context.Context, address string, remoteTSelector []byte) (*mms.Client, error) {
    return mms.Dial(ctx, address, mms.Config{
        RequestTimeout: 2*time.Second, // 读写请求；连接/协商由ctx及ISO.HandshakeTimeout控制。
        MaxPDUSize: 65000,
        ISO: mms.ISOConfig{RemoteTSelector: remoteTSelector},
    })
}
```

选择子是字节，不是host:port字符串。nil使用库默认值，非nil空切片表示空选择子；Local/Remote的T、S、P选择子及APTitle按设备实际配置提供，不要靠试错随意更改。Dial的ctx不会自动绑定整个Client生命周期，仍需Close或Conclude。

## 一次读取多个变量

批量调用有两层错误：返回的`err`针对整个请求，`results[i].Error`针对第i个变量。逐项结果顺序与访问列表一致。

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
            fmt.Printf("%s 读取失败: %v\n", accesses[i].Name, result.Error)
            continue
        }
        fmt.Printf("%s = %s (type=%s)\n", accesses[i].Name, result.Value, result.Value.Kind())
    }
    return nil
}
```

`WriteMultiple(ctx, accesses, values)`同样返回`([]error,error)`，每一项的nil表示该项写入成功。不要把部分写入成功当成事务回滚。可运行版本：[read-batch](../../examples/mms/read-batch/main.go)。

## 读取数组元素或结构成员

```go
func ReadParts(ctx context.Context, client *mms.Client) error {
    // 下标从0开始；这里读取第二个数组元素。
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

写同一位置使用`WriteAccess(ctx, access, value)`。`Element`/`Member`表示单层AlternateAccess；不是所有设备都支持，失败时应处理返回的访问错误。完整数组或结构可直接`Read(ctx, name)`，再用`Value.Elements()`解析。

## 发现 Domain、变量名和类型

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

`Names`自动拼接所有分页；需要控制每页时用`GetNameList(ctx,class,scope,domain,after)`，下一页的`after`取上一页最后一个名字，直到`MoreFollows=false`。`spec`描述设备声明的宽度、数组长度和结构成员，不要用当前值的大小猜类型。运行：[browse](../../examples/mms/browse/main.go)。

## 按命名变量表成组读写

这个场景建立AA命名变量表，作用域仅限当前关联，断开后消失。动态MMS列表与IEC61850的SCL数据集/报告控制块不是同一个API；对端也可能拒绝动态定义。

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
        if failure != nil { return fmt.Errorf("写列表第%d项: %w", i, failure) }
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

已有列表可直接`ReadNamedList`。`GetNamedListAttributes`返回成员与`Deletable`；删除操作的`DeleteResult`区分匹配数与删除数。运行：[data-set](../../examples/mms/named-list/main.go)。

## 接收和发送 InformationReport

客户端通过`Reports()`接收服务器主动发来的原始MMS报告。下面函数等待一条报告并允许调用方取消：

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

逐项检查`report.Results[i].Error`再取Value。显式变量报告有`Variables`；命名列表报告使用`List`，此时按该列表的已知成员顺序解释Results，不能假设Variables非空。应用不要关闭这个只读通道；报告队列满会关闭关联并返回`ErrReportOverflow`。

服务端从`Server.OnAssociation`取得每个连接的Association，通过它给对应客户端发送：

```go
func SendMeasurement(ctx context.Context, association *mms.Association, value int64) error {
    report := mms.InformationReport{
        Variables: []mms.VariableAccess{mms.Access(mms.DomainName("sample", "int"))},
        Results: []mms.ReadResult{{Value: mms.Integer(value)}},
    }
    return association.SendReport(ctx, report)
}
```

回调应使用`association.Context()`控制生命周期，并在`Serve`前设置。这个API不会自动订阅设备值，也没有配置BRCB/URCB。能直接运行的两端例子：[report](../../examples/mms/report/main.go)。

## 创建自己的 MMS 服务端

用Model注册变量，或实现Store把读写接入业务。下面函数运行到ctx取消，再关闭并等待连接处理结束：

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

`Writable`默认false。Model并发安全，写入会按TypeSpec检查；业务Store也必须并发安全并响应context。运行端口0的本机版本见 [mmsdemo/server.go](../../examples/internal/mmsdemo/server.go)。

## 请求超时、错误和并发调用

```go
func ReadWithDeadline(parent context.Context, client *mms.Client, name mms.ObjectName) (mms.Value, error) {
    ctx, cancel := context.WithTimeout(parent, time.Second)
    defer cancel()
    value, err := client.Read(ctx, name)
    if errors.Is(err, context.DeadlineExceeded) {
        return mms.Value{}, fmt.Errorf("读取%s超时: %w", name, err)
    }
    return value, err
}
```

| 情况 | 应用如何处理 |
|---|---|
| `errors.Is(err,mms.ObjectNonExistent)` | 检查Domain/item或重新发现目录 |
| `errors.Is(err,mms.ObjectAccessDenied)` | 处理只读或对端权限，不假设写入成功 |
| `errors.Is(err,mms.TypeInconsistent)` | 按变量声明类型构造Value |
| `*mms.ServiceError` / `*mms.RejectError` | 用`errors.As`取得服务级class/code或拒绝原因 |
| 已发送请求超时/取消、`ErrProtocol`、断连 | 本实现关闭关联以隔离迟到响应；重新Dial后再继续业务 |
| 等待在途窗口时取消 | 尚未发送请求，不因此关闭关联 |

同一个Client允许并发读写，内部按InvokeID分派响应并限制在途数。正常释放前先等待业务请求结束；不要与正在进行的请求并发调用Conclude。

## 调用速查

| 目的 | 方法及返回值 |
|---|---|
| 身份/状态 | `Identify(ctx) (Identity,error)`、`Status(ctx) (Status,error)` |
| 读一个/多个 | `Read(ctx,name) (Value,error)`、`ReadMultiple(ctx,accesses...) ([]ReadResult,error)` |
| 写一个/多个 | `Write(ctx,name,value) error`、`WriteMultiple(ctx,accesses,values) ([]error,error)` |
| 选择一部分 | `ReadAccess` / `WriteAccess` + `Element` / `Member` |
| 目录和类型 | `Names`、`GetNameList`、`GetVariableAccessAttributes` |
| 命名列表 | `DefineNamedList`、`GetNamedListAttributes`、`ReadNamedList`、`WriteNamedList`、`DeleteNamedList` |
| 报告/连接状态 | `Reports()`、`Done()`、`Err()` |
| 释放/中止 | `Conclude(ctx) error`、`Close() error` |

[Value类型转换](data.zh-CN.md) · [API目录](README.zh-CN.md)
