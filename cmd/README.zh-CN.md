# 命令行工具

[English](README.md) | 简体中文

这里是可运行的网络工具。学习如何在业务代码里调用库，直接看 [API手册](../docs/api/README.zh-CN.md) 或 [按场景拆分的示例](../examples/README.zh-CN.md)。

## MMS：一个服务端，一个客户端

```bash
go run ./cmd/mms-server -address 127.0.0.1:8102
```

另一个终端：

```bash
go run ./cmd/mms-client -address 127.0.0.1:8102 -item int -write-int 99
```

客户端支持-domain、-item、-write-int和-pdu。服务端提供sample模型，源码在internal/models/mms。所有参数用-h查看。

## GOOSE/SV：离线完整帧

```bash
go run ./cmd/goose-publisher -output-frame _local/frames/goose.bin
go run ./cmd/goose-subscriber -input-frame _local/frames/goose.bin
go run ./cmd/sv-publisher -output-frame _local/frames/sv.bin
go run ./cmd/sv-subscriber -input-frame _local/frames/sv.bin
```

## 接真实接口

先`go run ./cmd/sv-subscriber -list`列出接口，用实际名称替换eth0。在两个终端运行：

```bash
go run ./cmd/sv-subscriber -interface eth0 -vlan 123 -count 3 -duration 5s
go run ./cmd/sv-publisher -interface eth0 -vlan 123 -interval 20ms -duration 1s
```

GOOSE使用对应工具和-min/-max重传参数。-vlan -1发送/限定无标签；订阅默认-vlan -2接受有/无标签。Windows不投递目标组播时给订阅工具加-promiscuous。-capture写PCAP，-count到期不足数量会失败退出。SV默认-sync 0表示未同步。

[平台与权限](../docs/platforms.zh-CN.md)。六个工具通过scripts/build构建到_local/bin；`cmd/internal`是参数、抓包与示例模型辅助，不是业务API。
