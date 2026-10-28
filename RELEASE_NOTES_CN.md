# Forward 发布说明

## v1.1.0 · 2026-10-28

### 本次更新

- Windows「接口测试」标签页新增 TCP 带宽测试，可设置 1～3600 秒的发送时长，默认 10 秒。
- 显示实时/平均 Mbps、传输量，以及接收端回报的实际接收字节数和平均速率。
- 支持经过 Forward 转发链路测速；发送完成后自动结束，监听端继续等待下一次连接。
- CLI 新增 `-bandwidth` 和 `-duration` 参数，Linux/macOS 可作为测速对端。两端均需使用新版 Forward 并启用带宽模式。
- 改进测试连接跟踪和停止流程，支持取消阻塞发送及重复启动。
- 新增统一多平台构建入口，按 Windows 架构生成图标/manifest 资源，并输出 SHA256 校验文件。

### 发行文件

| 系统 | 架构 | 文件 |
| --- | --- | --- |
| Windows | amd64 / 386 | `forward_windows_amd64.exe` / `forward_windows_386.exe` |
| Linux | amd64 / 386 | `forward_linux_amd64` / `forward_linux_386` |
| Linux | arm64 / armv7 | `forward_linux_arm64` / `forward_linux_armv7` |
| macOS | amd64 / arm64 | `forward_darwin_amd64` / `forward_darwin_arm64` |

下载对应系统与架构的文件；Linux/macOS 下赋予执行权限后运行。`SHA256SUMS` 包含上述 8 个可执行文件的 SHA256。

### 带宽测试示例

```bash
# 接收端
./forward_linux_amd64 -probe listen -addr 0.0.0.0:9999 -proto tcp -bandwidth
# 发送端：替换为实际接收端地址
./forward_linux_amd64 -probe send -addr 192.0.2.10:9999 -proto tcp -bandwidth -duration 10
```

### 验证与兼容性

- 使用 Go 1.20.14 构建全部 8 个目标，以保留 Windows Server 2012 兼容性。
- 已通过竞态检测测试、宿主机及 Windows amd64 静态检查。
- 已验证直连/经过转发器的测速、重复启动、取消阻塞发送、错误对端、传输中断及 TCP/UDP echo 回归。
- macOS ARM64 CLI 收发、实时统计、参数校验与 SIGTERM 停止已验证。
- Windows GUI 和其他目标系统尚未实机验证；Windows ARM64 暂不提供。测速使用 Forward 专用 TCP 协议，不兼容 iperf 或普通 echo 监听端。

## 项目概览

Forward 是一个使用 Go 编写的轻量级 TCP/UDP 端口转发工具。Windows 提供图形界面，Linux 和 macOS 提供命令行模式。

### 主要功能

- 支持 TCP、UDP 和双协议转发。
- 支持单端口及端口范围映射，支持范围端口按序对应。
- UDP 会话按客户端地址聚合，并按空闲超时自动回收。
- 支持全局和规则级 ACL，可使用单个 IP 或 CIDR 配置允许与拒绝列表。
- 提供连接数、会话数、流量、速率和拒绝次数统计。
- Windows GUI 支持规则编辑、配置保存、启动/停止和会话查看。
- 内置 `-probe` 连通性测试模式，支持 TCP/UDP 监听、发送和回显。
- 编译后为单文件程序，可选使用 UPX 压缩。

### 构建要求

- 使用 Go 1.20.x 构建，以保持 Windows Server 2012 兼容性。
- Linux/macOS：运行 `./build.sh`；使用 `./build.sh upx` 启用压缩。
- Windows：运行 `build.bat` 构建 GUI；运行 `build.bat linux` 同时生成 Linux 版本。
- 多平台：使用 Go 1.20.14 运行 `go run ./tools/build`，或运行 `./build.sh all` / `build.bat all`。

### 使用示例

```bash
./forward -config forward.json
./forward -listen 0.0.0.0:19100 -target 192.0.2.10:19100 -proto both
./forward -probe listen -addr 0.0.0.0:9999 -proto tcp -echo
```

### 注意事项

部署前请检查监听地址、目标地址以及 `allow`、`deny` 和 `defaultPolicy` 配置。可运行 `go test -race ./...`、`go vet ./...`，并使用内置 probe 完成实际链路验证。
