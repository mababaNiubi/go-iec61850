# IEDLab 应用入口

[English](README.md) | 简体中文

仓库统一构建为一个 `iedlab` 程序，提供浏览器工作台和命令行。

```sh
go run ./cmd/iedlab serve -http 127.0.0.1:8080
```

打开 `http://127.0.0.1:8080`，导入 SCL 或载入示例，即可浏览测点并启动 IED 模拟。前端随 Go 程序一起提供。[工作台使用指南](../docs/workbench.zh-CN.md)

| 命令 | 用途 |
|---|---|
| `iedlab serve [-file model.scd]` | 网页界面、HTTP API、模拟器管理、远端 MMS 客户端 |
| `iedlab simulate -file model.scd -ied IED -ap AP` | 无界面的 SCL 设备模拟，支持 MMS 和 GOOSE |
| `iedlab inspect -file model.scd` | 查询接入点、路径、数据集、GOOSE 和 ExtRef |
| `iedlab protocol <工具>` | 底层 MMS/GOOSE/SV 调试 |

`protocol` 子命令包括 `mms-client`、`mms-server`、`goose-publisher`、`goose-subscriber`、`sv-publisher`、`sv-subscriber`，原有选项可通过 `-h` 查看。

```sh
go run ./cmd/iedlab simulate -ied BayIED -ap S1 -listen 127.0.0.1:8102
go run ./cmd/iedlab inspect -file internal/sample/bay.scd -ied BayIED -ap S1 -match XCBR1.Pos
go run ./cmd/iedlab protocol sv-subscriber -list
```

`cmd/internal` 放置底层诊断的参数、抓包和示例模型辅助。协议库和[独立 API 示例](../examples/README.zh-CN.md)按各自用途组织。
