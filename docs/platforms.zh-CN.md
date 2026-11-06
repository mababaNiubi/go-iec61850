# 平台接入与已知边界

[English](platforms.md) | 简体中文

协议核心为纯Go。MMS使用TCP；GOOSE/SV使用Ethernet，127.0.0.1不能代替二层测试。

| 平台 | 接入 | 要求 |
|---|---|---|
| Windows amd64 | Npcap DLL运行时调用，无cgo | 安装可访问的Npcap驱动/DLL；接口必须是Ethernet |
| Linux | 非阻塞AF_PACKET/SOCK_RAW | CAP_NET_RAW或相应权限；显式接口名 |
| 其他未支持平台 | 返回ErrUnsupported | 可自行提供FrameIO接入协议状态机 |

Ethernet.Open不自动选择网卡。Windows接受Npcap名称、完整描述或列表索引；Linux接受接口名。Loopback的链路类型不适用于GOOSE/SV，程序会拒绝。用各订阅示例的 `-list` 查看接口。

## Windows组播投递

非混杂捕获不等于已经加入目标组播地址表。部分接口在该模式下不会投递IEC61850组播；本项目的跨虚拟机测试中曾实际遇到。可在专用接口上显式使用 `-promiscuous`，库中对应 `ethernet.Config{Promiscuous: true}`。协议和VLAN过滤仍然生效。

## VLAN字节保真

编解码器保留PCP、DEI和完整VID。Linux接入使用PACKET_AUXDATA重建被内核/设备剥离的标签，但只能使用操作系统实际提供的信息。

- 旧4.19内核的测试接收路径将DEI1清除，独立原始socket探针同样观察到；不能从丢失后的元数据推断原位。
- 一条Windows→虚拟机链路将发送PCP4变成接收PCP0；反向保留PCP4。该变化不能由协议解码器恢复。
- Linux veth的DEI0测试严格保留高PCP和高VID；DEI1作为独立能力探针，默认严格检查。只有显式设置 `ETHERNET_LIVE_ALLOW_DEI_LOSS=1` 且其他字节完全一致时，已知DEI-only差异才记为SKIP。

因此，协议互通通过不等同于所有VLAN字段在指定硬件链路中都保真。部署时应比较两端抓包和操作系统元数据。历史原始证据保存在本地 `_local/reports/`。

## 调度、同步与关闭

GOOSE按配置重传并使用TAL；SV按软件时间槽发送，迟到时跳过过期槽并推进计数。Go调度、系统负载、驱动和网卡会影响延迟，库没有保护级硬实时承诺。

SV示例默认报告未同步；没有建立PTP或硬件采样时钟。SampleSync、SampleRate或SampleMode只是报文字段，不会创建同步源或采样硬件。

原生读取使用非阻塞轮询，支持context取消和协调关闭。注入调用进入操作系统后无法安全强制中断，写入取消在调用前检查。实际时延、远端IED、物理交换机和硬件offload需要对应环境测试。
