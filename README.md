# go-iec61850

原生Go的MMS客户端/服务端、普通二层GOOSE和SV协议库。Go1.24+，运行库无第三方Go依赖，不需要cgo。

## 我想连接设备，读写一个变量

先在另一个终端启动示例对端：`go run ./cmd/mms-server -address 127.0.0.1:8102`。业务代码的主要调用如下，放在返回error的函数中执行：

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

`mms`导入路径为`github.com/mababaNiubi/go-iec61850/mms`，上例还使用标准库context、time和fmt。**[完整可复制程序、参数和错误处理](docs/api/mms.md#连接设备并读写一个整型变量)**。

不想自己启动服务器，直接运行已注释的示例：

```bash
go run ./examples/mms/read-write
```

默认使用临时本机服务；加`-address host:port`可连接现有的sample模型对端。

## 按业务场景找API

| 想做什么 | 文档 |
|---|---|
| 读写变量、批量读取、数组/结构、目录、命名列表、报告、服务端 | [MMS API](docs/api/mms.md) |
| 把MMS Value转换成Go整数、布尔、浮点、字符串、数组或时间 | [值与类型](docs/api/data.md) |
| 发布状态量，数据变化后更新，订阅并判断TAL失效 | [GOOSE API](docs/api/goose.md) |
| 发布/订阅采样值、多ASDU、计数和字节布局 | [SV API](docs/api/sv.md) |

[API总目录](docs/api/README.md) · [八个独立示例](examples/README.md) · [现成命令行工具](cmd/README.md)

## 示例和工具分别在哪

- `examples/`：一目录一个业务场景，每个main.go直接展示API调用和关键注释。MMS默认本机回环，其余使用内存链路，均不向真实网卡发包。
- `cmd/`：六个命令行工具，负责参数、真实设备连接、网卡、离线帧和PCAP。
- `mms/`、`data/`、`goose/`、`sv/`、`ethernet/`：应用实际导入的库。
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

工具输出到`_local/bin/<系统>-<架构>/`，生产构建使用CGO_ENABLED=0和trimpath。Windows真实网卡需要Npcap，Linux需要raw socket权限，详见 [平台说明](docs/platforms.md)。[开发与验证](docs/development.md) 记录本地C对照入口。

当前为MMS核心和普通L2 GOOSE/SV，尚未实现完整ACSI/SCL、报告控制块、R-GOOSE/R-SV、PTP或安全扩展。软件采用 **GPL-3.0-or-later**，见 [LICENSE](LICENSE) 和 [NOTICE](NOTICE)。
