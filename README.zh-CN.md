# go-iec61850

[English](README.md) | 简体中文

原生Go的MMS客户端/服务端、普通二层GOOSE和SV协议库。Go1.24+，运行库无第三方Go依赖，不需要cgo。

## 打开浏览器工作台

```sh
go run ./cmd/iedlab serve -http 127.0.0.1:8080
```

打开 `http://127.0.0.1:8080`，导入 SCL 或载入示例，再选择 IED。可以浏览逻辑节点、修改采集值、启停 MMS/GOOSE 模拟，并用内置 MMS 客户端连接设备。前端嵌入 Go 程序。[工作台使用指南](docs/workbench.zh-CN.md)

## 用 SCD 启动设备和监控

从当前工作树运行 `go run ./examples/scd/breaker`，可以看到断路器 MMS 采集、闭锁、分合闸以及 GOOSE 反馈。`go run ./cmd/iedlab inspect -file internal/sample/bay.scd` 可离线列出接入点。SCD 集成随 v1.1.0 发布，使用 `go get github.com/mababaNiubi/go-iec61850@v1.2.0` 安装。[SCD API 和常用路径](docs/api/scl.zh-CN.md) · [设备与监控示例](examples/scd/breaker/README.zh-CN.md)

## 我想连接设备，读写一个变量

先在另一个终端启动示例对端：`go run ./cmd/iedlab protocol mms-server -address 127.0.0.1:8102`。业务代码的主要调用如下，放在返回error的函数中执行：

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

client, err := mms.Dial(ctx, "127.0.0.1:8102", mms.Config{})
if err != nil { return err }
defer client.Close()

name := mms.DomainName("sample", "int")
if err := client.Write(ctx, name, mms.Integer(99)); err != nil { return err }
value, err := client.Read(ctx, name)
if err != nil { return err }
number, err := value.Int()
if err != nil { return err }
fmt.Println("sample/int =", number)

return client.Conclude(ctx)
```

`mms`导入路径为`github.com/mababaNiubi/go-iec61850/mms`，上例还使用标准库context、time和fmt。**[完整可复制程序、参数和错误处理](docs/api/mms.zh-CN.md#连接设备并读写一个整型变量)**。

不想自己启动服务器，直接运行已注释的示例：

```bash
go run ./examples/mms/read-write
```

默认使用临时本机服务；加`-address host:port`可连接现有的sample模型对端。

## 按业务场景找API

| 想做什么 | 文档 |
|---|---|
| 设备控制、URCB 订阅、嵌入模拟器和 SCL 定义的 SV 布局 | [设备接入 API](docs/api/ied.zh-CN.md) |
| 读写变量、批量读取、数组/结构、目录、命名列表、报告、服务端 | [MMS API](docs/api/mms.zh-CN.md) |
| 把MMS Value转换成Go整数、布尔、浮点、字符串、数组或时间 | [值与类型](docs/api/data.zh-CN.md) |
| 发布状态量，数据变化后更新，订阅并判断TAL失效 | [GOOSE API](docs/api/goose.zh-CN.md) |
| 发布/订阅采样值、多ASDU、计数和字节布局 | [SV API](docs/api/sv.zh-CN.md) |

[API总目录](docs/api/README.zh-CN.md) · [应用示例](examples/README.zh-CN.md) · [现成命令行工具](cmd/README.zh-CN.md)

## 示例和工具分别在哪

- `examples/`：一目录一个业务场景，每个main.go直接展示API调用和关键注释。MMS默认本机回环，其余使用内存链路，均不向真实网卡发包。
- `cmd/iedlab/`：统一的 `serve`、`simulate`、`inspect` 和 `protocol` 命令。
- `internal/workbench/`：工作区、HTTP API 和嵌入式前端；协议生命周期使用公开 `simulator` 包。
- `internal/sample/`：工作台和示例共用的 SCL 文件。
- `mms/`、`data/`、`goose/`、`sv/`、`ethernet/`、`scl/`、`ied/`、`simulator/`：应用实际导入的库。
- `_local/`：被Git忽略的C对照、标准资料、报告和产物。

## 构建与验证

```powershell
./scripts/build.ps1
./scripts/test.ps1 -Race -Examples
```

```bash
bash scripts/build.sh
bash scripts/test.sh --race --examples
```

工具输出到`_local/bin/<系统>-<架构>/`，生产构建使用CGO_ENABLED=0和trimpath。Windows真实网卡需要Npcap，Linux需要raw socket权限，详见 [平台说明](docs/platforms.zh-CN.md)。[开发与验证](docs/development.zh-CN.md) 记录本地C对照入口。

v1.2.0 提供 MMS、普通 L2 GOOSE/SV、设备控制客户端、URCB 订阅、嵌入模拟器和[限定范围的 SCL 模型映射](docs/api/ied.zh-CN.md)。完整 ACSI/SCL、RCB 服务端自动生成、BRCB 重放、R-GOOSE/R-SV、PTP 和安全扩展仍超出当前范围。软件采用 **GPL-3.0-or-later**，见 [LICENSE](LICENSE) 和 [NOTICE](NOTICE)。
