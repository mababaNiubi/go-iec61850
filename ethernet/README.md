# Ethernet接入

GOOSE/SV的可选原始Ethernet适配。库核心依赖FrameIO接口，也可以使用内存链路或自行实现传输。

```go
conn, err := ethernet.Open(ethernet.Config{
    Interface: "eth0", // 用Interfaces()返回的实际接口替换
    EtherType: sv.EtherType,
})
if err != nil { return err }
defer conn.Close()
```

Windows amd64从系统Npcap目录加载DLL，不使用cgo；Linux使用非阻塞AF_PACKET、socket局部组播成员关系和PACKET_AUXDATA。Linux打开raw socket需要CAP_NET_RAW或相应权限。其他未支持平台返回ErrUnsupported。

接口必须显式指定；Windows支持Npcap名称、完整描述或列表索引，Linux使用接口名。Conn.Interface()/MAC()返回选中接口及源地址。Loopback等非Ethernet链路会被拒绝。

读取通过非阻塞轮询支持context与协调Close；返回帧拥有独立字节。帧包含Ethernet头和可选padding，排除FCS。EtherType过滤识别无标签、单层和双层VLAN，具体协议编解码器仍按自身profile验证。

部分Windows接口需要Promiscuous才能投递尚未注册到地址表的组播；订阅示例提供显式 `-promiscuous`。Linux可重建的VLAN位取决于内核实际提供的元数据；旧内核DEI和部分虚拟链路PCP有实测限制，见 [平台说明](../docs/platforms.md)。

普通单元测试使用模拟驱动，不自动选择网卡。真实接口测试需要显式环境变量；在专用测试接口运行，参见 [开发与验证](../docs/development.md)。
