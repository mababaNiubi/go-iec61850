# 快速 API 指引

[English](quickstart.md) | 简体中文

[项目概览](../../README.zh-CN.md) · [API 索引](README.zh-CN.md) · [MMS 详细指引](mms.zh-CN.md)

本教程用 Go 客户端连接本机 MMS 服务，写入整型值后读回。需要 Go 1.24+ 和两个终端，
不需要真实 IED、抓包驱动或 C 库。下面分别说明命令在 SDK 仓库还是业务模块中执行。

## 1. 启动示例服务

终端 A：如果尚未下载 SDK，先克隆仓库：

```sh
git clone https://github.com/mababaNiubi/go-iec61850.git
cd go-iec61850
go run ./cmd/iedlab protocol mms-server -address 127.0.0.1:8102
```

保持终端运行。示例提供可写整型变量 `sample/int`。端口被占用时，更换端口并同步修改
客户端地址。若只想体验自动管理的对端，可直接在仓库执行 `go run ./examples/mms/read-write`。

## 2. 创建自己的业务模块

终端 B：在 SDK 仓库之外创建一个新的空目录：

```sh
mkdir iec61850-demo
cd iec61850-demo
go mod init example.com/iec61850-demo
go get github.com/mababaNiubi/go-iec61850@v1.4.0
```

业务程序引用已发布的模块，不需要 `replace`、相邻 SDK 源码或 `go.work`。

## 3. 编写并运行客户端

将以下完整程序保存为 `iec61850-demo/main.go`：

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
		log.Fatal(err)
	}
}

func run() error {
	// This deadline bounds the entire short demonstration.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := mms.Dial(ctx, "127.0.0.1:8102", mms.Config{})
	if err != nil {
		return err
	}
	defer client.Close()

	name := mms.DomainName("sample", "int")
	if err := client.Write(ctx, name, mms.Integer(99)); err != nil {
		return err
	}
	value, err := client.Read(ctx, name)
	if err != nil {
		return err
	}
	number, err := value.Int()
	if err != nil {
		return err
	}
	fmt.Printf("sample/int = %d\n", number)
	return nil
}
```

在业务模块目录执行：

```sh
go run .
```

预期输出：

```text
sample/int = 99
```

程序在正常和错误返回时都会关闭关联。结束后用 Ctrl+C 停止示例服务。本例验证示例变量
的写入读回；设备动作需通过建模控制和独立状态反馈确认。

## 4. 理解接口约定

| 调用 | 含义 |
|---|---|
| `mms.Dial(ctx, address, cfg)` | 连接并协商，地址为 `host:port`。ctx 限制建立阶段，后续操作各自传入 ctx。`mms.Config{}` 选择有界默认值。 |
| `defer client.Close()` | 在所有返回路径释放连接。请求结束后可选择 `Conclude(ctx)` 正常协商释放。 |
| `mms.DomainName(domain, item)` | 构造远端 MMS 对象名；`sample`、`int` 来自本例服务，不是任意本机变量名。 |
| `mms.Integer(99)` | 构造有符号协议值。按远端声明选择类型，不会把任意 Go 数值自动转换成目标类型。 |
| `client.Write` / `client.Read` | 发起确认服务并检查错误；写入提交后超时，结果可能不确定。 |
| `value.Int()` | 返回 `int64` 或类型不匹配错误。其他转换包括 `Bool`、`Uint`、`Float`、`StringValue`、`Time`。 |

示例的五秒 ctx 限制整个短流程。长期程序应为每次操作创建有期限的 ctx。
默认 `RequestTimeout` 为每个确认请求五秒；完整限制可用
`go doc github.com/mababaNiubi/go-iec61850/mms.Config` 查询。

## 5. 接入工程化设备

使用设备或 SCL 给出的实际地址、Domain 和类型声明。FC 为 `ST` 的相对引用
`XCBR1.Pos.stVal` 对应 MMS 项 `XCBR1$ST$Pos$stVal`，可用
`ied.ObjectName(domain, fc, reference)` 转换。示例的可写 `sample/int` 不代表设备控制路径。

| 业务任务 | 公开接口 | 指引和可运行程序 |
|---|---|---|
| 无 SCL 在线发现 | `ied.Dial`、`Client.Discover` | [在线发现与快照](engineering.zh-CN.md#在线发现报告历史与现值)、[GoDoc 示例](../../ied/example_test.go) |
| 批量读取 | `mms.Client.ReadMultiple` | [MMS 指引](mms.zh-CN.md)、[read-batch](../../examples/mms/read-batch/main.go) |
| 接收 URCB/BRCB | `ied.Client.SubscribeURCB`、`SubscribeBRCB` | [设备 API](ied.zh-CN.md)、[历史续传](engineering.zh-CN.md#断线后续传-brcb) |
| 读取当前数据集 | `Client.ReadDataSet`、`ReportSubscription.ReadSnapshot` | [现值与历史](engineering.zh-CN.md#在线发现报告历史与现值) |
| 操作设备 | `ied.Client.Operate` | [控制接口](ied.zh-CN.md)、[完整程序](../../examples/ied/control-report/main.go) |
| 接入采集和执行器 | `scl.ParseFile`、`System.Device`、`IED.Update`、`IED.OnControl` | [SCL 指引](scl.zh-CN.md)、[整站程序](../../examples/scd/station/main.go) |
| 使用 GOOSE/SV | `goose`、`sv` 的布局、发布者、接收器 | [GOOSE](goose.zh-CN.md)、[SV](sv.zh-CN.md)、[示例](../../examples/README.zh-CN.md) |

批读同时检查请求错误与逐项 `ReadResult.Error`。订阅及时消费 `Events()`，通道关闭后检查
`Err()`；先停订阅，再关闭客户端。`ied.Client` 独占底层报告分发，不要再读取
`client.MMS().Reports()`。

当前快照没有 EntryID；完整 BRCB 报告经业务持久处理后才保存 EntryID。协议受理与增强终结
不代替实际 ST 反馈。详细约定见 [API 索引](README.zh-CN.md#名称生命周期与结果约定)。

## 常见问题

| 现象 | 检查项 |
|---|---|
| 连接被拒绝 | 服务终端是否运行，两端地址和端口是否一致。 |
| 对象不存在或拒绝访问 | 示例是否使用 `sample/int`；真实设备是否有该声明和权限。 |
| 类型不匹配 | 用 `GetVariableAccessAttributes` 查询声明，选择匹配的构造器和访问器。 |
| 请求超时 | 检查连接和 ISO/ACSE 参数；提交过的写入先确认结果，再决定后续操作。 |
| 没有报告 | 使用已配置并分配给本客户端的 RCB，及时消费队列；订阅成功不保证立即有事件。 |
| 没有物理 GOOSE/SV 帧 | 检查网卡、权限/Npcap、MAC/APPID/VLAN 和设备发布配置；MMS 示例服务不发布这些流。 |

反馈问题时，请提供版本、系统/架构、操作和错误，以及脱敏最小程序，提交到
[GitHub Issues](https://github.com/mababaNiubi/go-iec61850/issues)。
