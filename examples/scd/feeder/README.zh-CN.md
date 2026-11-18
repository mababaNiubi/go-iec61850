# 馈线采集完整流程（EX-01）

一个只用公开包、可直接运行的程序，覆盖常规站控采集全流程。建模的间隔
（XCBR 位置/控制、MMXU 测量、一个 BRCB）通过回环 TCP 提供服务，客户端依次演示：

- **验证绑定** —— `BindDataSet` 逐读复查声明（`CFG-01`）：远端重配置后拒绝
  读取，绝不按旧代际解码。
- **缓存报告** —— `SubscribeBRCB` + 总召；报告先写入调用方自有的存储，
  成功之后才保存 `EntryID` 游标。
- **控制与反馈** —— `Operate` 走真实 MMS；应用通过 `scl.IED.OnControl`
  绑定自己的执行器，用 `Update` 独立回传位置/品质/时标。拒绝或不确定的
  结果原样上报，绝不自动重试。
- **动态数据集** —— 创建、绑定、重配置并观察类型化的
  `ErrConfigurationChanged`，再 `Refresh` 到新代际。
- **重连** —— 保存的选点文档重新验证（`VerifySelection`），已确认的游标
  恢复 BRCB 订阅。

```sh
go run ./examples/scd/feeder
```

把执行器回调和存储切片换成你的设备驱动和数据库即可。基于同一套服务的
浏览器工作流见[工作台指南](../../../docs/workbench.zh-CN.md)。
