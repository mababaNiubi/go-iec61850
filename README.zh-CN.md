# go-iec61850

原生 Go 实现的 IEC 61850 MMS、二层 GOOSE 和采样值协议库，提供 SCL 模型接入与 IEDLab 浏览器工作台。

[English](README.md) | 简体中文

[快速开始](docs/api/quickstart.zh-CN.md) · [API 手册](docs/api/README.zh-CN.md) · [Go 参考](https://pkg.go.dev/github.com/mababaNiubi/go-iec61850) · [版本下载](https://github.com/mababaNiubi/go-iec61850/releases) · [更新记录](CHANGELOG.md)

SDK 用于监控程序和设备驱动接入、模型驱动的 IED 模拟，以及工程化以太网流的发布订阅。IEDLab 使用相同的公开接口，提供 CLI 和嵌入式浏览器界面。

## 项目概览

项目提供两个入口：供业务程序使用的 **Go SDK**，以及独立调试和模拟工具 **IEDLab**。
业务程序可以直接导入 SDK，不需要运行工作台或依赖其内部包。

典型用途包括 SCADA/网关采集与控制、模型驱动的设备模拟，以及 GOOSE/SV 接入测试。
SCL 工程声明将逻辑设备、测点、数据集和网络流映射为经过类型检查的协议对象。
设备驱动、业务计算、倍率、品质接纳、重试和持久化由调用方提供。

主要特性：

- **原生 Go**：运行库不依赖 C 库、cgo 或第三方 Go 模块。
- **类型明确**：访问器检查类型，保留有符号性、64 位整数、浮点线上宽度及原始品质/时间表示。
- **模型驱动**：解析工程路径与有序数据集，通过公开回调接入采集和执行器。
- **并发与所有权清晰**：请求 ctx、独立运行时、有界报告队列和共享以太网接收器适合长期服务。
- **便于本机开发**：使用内存以太网链路和回环 MMS 对端，在接入物理设备前验证业务流程。

## 功能与范围

| 领域 | 已提供能力 |
|---|---|
| MMS | 原生客户端/服务端、目录查询、类型读写、批量与交替访问、命名变量表 |
| IED 客户端 | 在线发现、即时控制、URCB/BRCB 订阅与续传、当前数据集读取、定值组 |
| SCL | 支持范围内的 SCD/ICD/CID 模型、测点目录、数据集、一次设备、子网/ServerAt 和 ExtRef 解析 |
| GOOSE | 二层发布订阅、类型布局、重发与 TAL 超时 |
| SV | 二层发布订阅、多 ASDU、SCL 布局与边界检查 |
| 模拟 | 单设备和多 IED 软件站点，可替换采集、计算与执行器回调 |
| IEDLab | SCL 导入、模型浏览、模拟、在线报告/控制/定值和工程比较 |

运行库无第三方 Go 模块依赖，不需要 cgo。支持的模型和限制见 [SCL 指引](docs/api/scl.zh-CN.md)与[平台说明](docs/platforms.zh-CN.md)。完整 ACSI/SCL、在线 GoCB/SVCB 重配置、R-GOOSE/R-SV、PTP 和 IEC 62351 不在当前范围内。发布版本不代表取得 IEC 一致性认证。

## 环境要求

- SDK 和源码构建需要 **Go 1.24 或更高版本**。
- MMS 使用 TCP，回环示例不需要配置网卡。
- 物理 GOOSE/SV 需要 Linux 原始套接字权限，或 Windows amd64 与 Npcap。内存链路示例无需这些条件。见[平台支持](docs/platforms.zh-CN.md)。

## 安装

在已有 Go 模块中添加 SDK：

```sh
go get github.com/mababaNiubi/go-iec61850@v1.4.0
```

按需导入 `github.com/mababaNiubi/go-iec61850/mms`、`ied` 等公开包，参见[包选择](docs/api/README.zh-CN.md#包选择)。

无需 Go 环境时，可从 [v1.4.0](https://github.com/mababaNiubi/go-iec61850/releases/tag/v1.4.0) 下载 Windows amd64 或 Linux amd64 的 IEDLab 包，并用 `SHA256SUMS.txt` 校验。压缩包包含启动说明和 SCD 模型。

## 快速开始

### 运行本机客户端与服务端

```sh
git clone https://github.com/mababaNiubi/go-iec61850.git
cd go-iec61850
go run ./examples/mms/read-write
```

示例自动启动临时回环服务，输出 `sample/int: 42 -> 99`。无需真实 IED 或物理网卡。

### 调用 API

在仓库根目录的另一个终端启动并保持示例服务运行：

```sh
go run ./cmd/iedlab protocol mms-server -address 127.0.0.1:8102
```

将以下完整程序保存为已安装 SDK 的 Go 模块中的 `main.go`：

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

执行 `go run .`，预期输出 `sample/int = 99`。程序写入的是示例服务的变量；接入真实 IED 时需选择它声明的 Domain、FC、类型和访问权限。设备动作使用 `ied` 建模控制，并独立采集 ST 反馈。

[快速 API 指引](docs/api/quickstart.zh-CN.md)包含从新建模块开始的步骤、参数说明、常见错误和后续入口。

## IEDLab 工作台

在仓库根目录运行：

```sh
go run ./cmd/iedlab serve -http 127.0.0.1:8080
```

打开 `http://127.0.0.1:8080`，选择“整站示例 → 启动整站”，在软件 GOOSE/SV 总线上运行示例 IED。也可导入自己的 SCL 检查模型或配置模拟器。前端嵌入可执行文件。

[工作台指南](docs/workbench.zh-CN.md) · [CLI 参考](cmd/README.zh-CN.md) · [完整站点](examples/scd/station/README.zh-CN.md)

## 文档与示例

| 目标 | 入口 |
|---|---|
| 连接、读写与资源生命周期 | [快速 API 指引](docs/api/quickstart.zh-CN.md) |
| 按任务查找公开接口 | [API 手册](docs/api/README.zh-CN.md) |
| 导入工程并绑定采集/控制逻辑 | [SCL 接入](docs/api/scl.zh-CN.md) |
| 报告、续传与当前值监控 | [工程场景](docs/api/engineering.zh-CN.md) |
| 运行并改造完整程序 | [示例目录](examples/README.zh-CN.md) |
| 理解实现职责 | [架构](docs/architecture.zh-CN.md) |

## 开发与贡献

在仓库根目录执行基本检查：

```sh
go test ./... -count=1 -timeout=120s
go vet ./...
```

具备竞态检测支持和 C 编译器时，可执行 `go test -race ./...`。生产构建可使用 `CGO_ENABLED=0`。[开发说明](docs/development.zh-CN.md)包含构建、示例和可选的独立 C 互操作检查。

通过 [GitHub Issues](https://github.com/mababaNiubi/go-iec61850/issues)提交可复现问题，附版本、系统/架构、预期与实际行为，以及脱敏的最小模型或程序。提交 PR 时说明行为变化、按需增加回归测试并执行相关检查。请勿提交设备口令、私有工程、抓包或下载的标准正文。

公开库位于 `mms/`、`ied/`、`scl/`、`simulator/`、`data/`、`goose/`、`sv/` 和 `ethernet/`；`cmd/iedlab/` 是应用，`internal/` 是内部实现。被忽略的 `_local/` 存放对照资料、标准和产物，普通构建不依赖它。

## 许可证

**GPL-3.0-or-later**。完整条款和上游署名见 [LICENSE](LICENSE)、[NOTICE](NOTICE)。标准资料保留出版方版权，不随项目源码分发。
