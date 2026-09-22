# Forward 发布说明

## 当前开发版本

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

### 使用示例

```bash
./forward -config forward.json
./forward -listen 0.0.0.0:19100 -target 192.0.2.10:19100 -proto both
./forward -probe listen -addr 0.0.0.0:9999 -proto tcp -echo
```

### 注意事项

发布前请检查监听地址、目标地址以及 `allow`、`deny` 和 `defaultPolicy` 配置。当前仓库没有独立单元测试，建议使用 `go vet ./...` 和内置 probe 完成发布验证。
