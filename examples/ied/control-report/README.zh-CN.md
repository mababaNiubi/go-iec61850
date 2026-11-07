# 控制 IED 并接收 URCB 报告

[English](README.md) | 简体中文

从仓库根目录运行：

```sh
go run ./examples/ied/control-report
```

默认加载共享 BayIED SCL，注册虚拟执行器，通过公开的 `simulator` 启动设备，再由 `ied.Client` 发送实际 MMS 控制。输出区分协议确认与命令终结，并独立读取位置反馈。

连接已有 IED 并订阅分配给当前客户端的 URCB：

```sh
go run ./examples/ied/control-report -address 127.0.0.1:8102 -object simpleIOGenericIO/GGIO1.SPCSO1 -urcb LLN0.ControlEventsRCB01 -on=true
```

该命令会向指定地址发送实际控制。可选 URCB 必须存在，报告包含数据集和 ConfRev。本地示例只提供普通直接控制，不提供 URCB 服务端。接入自己的应用时保留公开 `ied` 调用，使用自己的工程模型和设备回调，无需引用工作台内部包。[API 与结果含义](../../../docs/api/ied.zh-CN.md)
