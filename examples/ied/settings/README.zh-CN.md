# SP 参数和定值组

[English](README.md)

```sh
go run ./examples/ied/settings
```

SCD 中 PTOC1.OpDlTmms 是普通 SP 延时参数，PTOC1.StrVal 是包含两组定值的 ASG。
服务端通过 OnSetpoint 校验普通 SP 的范围。客户端将第 2 组阈值确认成 25，此时
第 1 运行组仍为 10，然后显式激活第 2 组。

SP 回调中接设备配置驱动。设备业务从 SG 视图读取当前运行参数。示例把参数保存在
模型进程中，设备配置持久化由应用的存储生命周期处理。

该 SCD 已通过 2007B4 XSD 结构校验。[API 指引](../../../docs/api/engineering.zh-CN.md)
