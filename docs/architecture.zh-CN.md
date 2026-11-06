# 代码结构与数据流

[English](architecture.md) | 简体中文

生产库按协议分包，公开包可以分别导入。没有统一巨型入口，也不需要先创建整个IEC61850设备才能使用MMS或编解码器。

| 层 | 包 | 依赖 |
|---|---|---|
| 共享数据 | data | 内部BER工具 |
| MMS | mms | data、internal/iso、internal/ber |
| 二层GOOSE | goose | data、internal/ber |
| 二层SV | sv | internal/ber；seqData使用原始布局 |
| 工程设备模型 | scl | encoding/xml、mms、goose、data |
| 可选接入 | ethernet | 标准库和操作系统原始网卡API |
| 使用示例 | examples | 公开库、共享示例模型和示例专用辅助包 |

`internal/iso`负责TPKT/COTP/Session/Presentation/ACSE；`mms`只通过该层收发MMS PDU。Client按InvokeID分派并发响应，Server把请求映射到Store。Model是可直接使用的并发安全内存Store。

GOOSE/SV的Frame保存完整Ethernet字段，PDU/ASDU保存协议内容。Encode/Decode只处理线上表示；Publisher/Subscriber管理发送和接收状态。FrameIO接口把状态机和网卡分开；Clock/Timer可注入，用于确定性测试或应用调度。

`data.Value`作为不可变值使用；`TypeSpec`声明类型限制。Frame、PDU、ASDU含可变切片或指针，使用Clone取得防御性副本。SV的SampleData是应用显式管理的可变字节缓冲区，通过偏移访问；传入Publisher.UpdateData时会复制。

应用创建的FrameIO、网卡连接和Listener需要按各API约定关闭。MMS请求使用context；示例在退出时取消并等待后台任务。GOOSE/SV事件队列有界，溢出显式报错，不静默丢弃。

`cmd/`保存网络命令行工具，其模型和参数辅助在 `cmd/internal/`。`examples/`每个入口只展示一个API场景，`examples/internal/`仅提供可选的本机MMS对端和内存链路。应用直接使用公开库，无需引用这些内部演示包。

生产Go单元测试随包提交。参考C代码、跨语言测试和标准文件在Git忽略的 `_local/` 中，既不参与生产依赖，也不参与根模块的 `go test ./...`。
