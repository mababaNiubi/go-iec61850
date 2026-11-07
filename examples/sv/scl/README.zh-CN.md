# 按 SCL 模型定位 SV 采样字段

[English](README.md) | 简体中文

从仓库根目录运行：

```sh
go run ./examples/sv/scl
```

通过公开 SCL 解析器取得 `MUA/AP1/LD0/MSVCB01`：每 ASDU 为 64 字节的 4I4U 数据集，每帧两个 ASDU。示例按名称查电流和 q，用模型偏移写入数值，再经 `simulator.MemoryLink` 传递完整以太网帧，由 `sv.Receiver` 接收两个样本。

预期输出 smpCnt=0/1，对应电流原始值 12345/12346。它们是瞬时原始值，工程量倍率由应用处理。软件示例明确设置 smpSynch=0，并选择 `AnySynchronization`。

接实际装置时用 `sv.EtherType` 打开 `ethernet.Conn`，采用工程所需的正常同步策略，把链路传给 Receiver.Run。多条流注册到同一个 Receiver，避免同网卡启动多个竞争读取循环。[SCL/SV 与生命周期 API](../../../docs/api/ied.zh-CN.md)
