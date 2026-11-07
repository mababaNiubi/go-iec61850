# 一份 SCD 连接三个 IED 角色

[English](README.md)

```sh
go run ./examples/scd/station
```

从仓库根目录运行。程序加载 station.scd，通过共享软件以太网总线连接合并单元 MUA、
间隔 IED BayIED 和站控端 StationHMI。退出时取消并等待所有接收者和运行时。

| 接入位置 | 对接实际设备时修改的位置 |
| --- | --- |
| MUA 采样源 | Sample 按模型字段填电流、电压采样。将正弦生成器替换成采集驱动。 |
| BayIED 输入 | SVInput 按 SCD 的 I1～I3、U1～U3 ExtRef 找源流和字段。测量驱动连续处理采样及品质。 |
| MMXU1.TotW | 示例按工程约定的 1 mA、10 mV 倍率计算三相瞬时功率，再平均 80 点。结果、q、t 一起通过 Signal.Sample 提交。 |
| XCBR1.Pos | OnOperate 中替换执行器调用，通过独立采集获取真实位置反馈。 |
| StationHMI | MMS 订阅 events01，GOOSE 按 breakerPosition 的 ExtRef 接位置。把标准输出换成监控画面或事件存储。 |

预期功率约 6900 W，整数采样量化产生少量偏差。示例显式接受软件采样的 SampleSync=0。
Go 定时器提供演示节拍，物理采样、同步和保护性能由设备时钟及现场测试验证。
该 SCD 已通过 IEC 61850-6 2007B4 XSD 结构校验。

[工程 API 和缓冲报告](../../../docs/api/engineering.zh-CN.md)
