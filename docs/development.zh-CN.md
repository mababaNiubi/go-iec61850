# 开发、构建和验证

[English](development.md) | 简体中文

从仓库根目录执行命令。Go最低版本为1.24；运行库无第三方Go模块，正常构建不需要cgo。

## 日常检查

```bash
go test ./... -count=1 -timeout=120s
go test -race ./... -count=1 -timeout=120s
go vet ./...
```

`go test -race`需要受支持的系统/架构及本机C编译器。这与生产程序是否依赖cgo是两个构建场景；生产构建脚本显式使用CGO_ENABLED=0。

```powershell
./scripts/test.ps1 -Race -Examples
./scripts/build.ps1
./scripts/build.ps1 -TargetOS linux -TargetArch amd64
```

```bash
bash scripts/test.sh --race --examples
bash scripts/build.sh
GOOS=linux GOARCH=amd64 bash scripts/build.sh
```

测试脚本保存JSON结果到 `_local/reports/unit/`。`--examples`/`-Examples`运行九个应用示例，也可按示例路径独立运行。构建脚本以CGO_ENABLED=0和-trimpath输出 `_local/bin/<系统>-<架构>/iedlab` 应用程序，Windows加.exe。交叉编译只证明编译成功，不能替代目标系统运行。

修复协议问题时，先增加能复现错误的回归测试，验证错误类型、旧有效状态、超时/取消和资源回收，再修复实现。模糊测试例如：

```bash
go test ./internal/ber -run '^$' -fuzz FuzzParse -fuzztime 10s
```

Go单元测试不自动选择网卡。显式接口环境变量会启用平台接入检查或真实帧测试；仅对测试者指定的专用接口使用它们。

## 本地对照资料

`_local/`整个目录被Git忽略，并按用途组织：

| 路径 | 内容 |
|---|---|
| interop/reference | 固定libiec61850源码、提交信息、导出SHA-256清单 |
| interop/tests | C测试程序、C HAL和Go/C互通测试 |
| interop/scripts | C基准、离线和显式网卡验证脚本 |
| standards | 标准文件、来源索引和用户提供的资料 |
| reports | 测试日志、JSON摘要、PCAP和历史验证证据 |
| bin | 当前构建的程序 |
| archive | 迁移前文档/构建及初始化许可证 |

这些文件不随Git仓库分发，普通构建和Go测试不依赖它们。已有本地对照资料时，在独立模块中运行：

```powershell
cd _local/interop
./scripts/test.ps1 -BuildReference -Race
```

```bash
cd _local/interop
bash scripts/test.sh --build-reference --race
```

该模块通过 `replace github.com/mababaNiubi/go-iec61850 => ../..` 使用正在开发的生产库。C构建要求gcc/ar；参考版本固定v1.6.2.1，源码校验不通过则停止。真实网卡脚本必须显式指定接口；GOOSE与SV分别支持本地接口和可选peer接口。

## Git收录范围

提交生产Go代码、随包单元测试、fuzz回归语料、示例、通用文档、构建脚本和许可文件。`.gitignore`排除 `_local/`、IDE配置、环境文件、C代码、标准PDF、抓包、日志、缓存与二进制。

```bash
git status --short --untracked-files=all
git check-ignore _local/standards _local/interop/reference .idea
```

文档和示例使用相对仓库路径、回环地址或接口占位符。主机地址、登录凭据、真实Npcap标识和机器绝对路径应仅保存在被忽略的本地资料中。标准文件保留出版方版权，来源索引不构成完整正文已经核对或通过认证的声明。
