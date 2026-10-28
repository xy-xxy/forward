# Forward

一个轻量的 TCP / UDP 端口转发工具,使用 Go 编写,跨平台。Windows 下提供基于 [lxn/walk](https://github.com/lxn/walk) 的图形界面,Linux/macOS 下作为 CLI 守护进程运行。

## 特性

- **TCP / UDP / 双协议** 转发,单条规则可同时监听 TCP 与 UDP
- **端口范围** 支持,例如 `0.0.0.0:10000-10100 -> 1.2.3.4:10000-10100`
- **UDP 会话保持**,按客户端地址聚合,空闲超时自动回收
- **黑/白名单(ACL)**:支持单 IP 和 CIDR,可设全局或每条规则,支持默认策略
- **实时统计**:连接数、UDP 会话数、字节数、速率、拒绝次数,GUI 下还能看到每条会话的详情
- **TCP 带宽测试**:指定发送时长，查看实时/平均 Mbps、传输量和对端实际接收结果，支持经过转发链路测速
- **跨平台**:Windows GUI(单文件 exe)+ Linux / macOS CLI
- **零依赖运行**,编译产物单文件,UPX 压缩后通常 < 2 MB
- 兼容 **Windows Server 2012**(用 Go 1.20.x 编译即可)

## 目录结构

```
main.go                # 转发核心(Forwarder、TCP/UDP 处理、统计、会话表)
cli.go                 # 入口 + CLI 模式
gui_windows.go         # Windows GUI(walk)
gui_other.go           # 非 Windows 的 GUI 占位
console_windows.go     # Windows 下把 stdout 接回父 cmd
console_other.go       # 其他平台的占位
forward.json           # 示例配置
forward.manifest       # Windows manifest(DPI、版本号等)
rsrc_windows.syso      # 由 forward.manifest 生成的资源文件
build.bat / build.sh   # 编译脚本
```

## 构建

需要 Go **1.20.x**(为兼容 Windows Server 2012)。

### Windows

```bat
build.bat              :: 编译 forward.exe(GUI)
build.bat linux        :: 同时交叉编译出 Linux 版 forward
```

脚本会自动:
1. `go mod tidy`
2. 如缺 `rsrc_windows.syso`,用 [akavel/rsrc](https://github.com/akavel/rsrc) 从 `forward.manifest` 生成
3. `go build -trimpath -ldflags="-H windowsgui -s -w"` 编译
4. 若同目录有 `upx.exe` 则 LZMA 压缩

### Linux / macOS

```bash
./build.sh             # 仅编译
./build.sh upx         # 编译 + UPX 压缩(需先安装 upx)
```

### 多平台、多架构构建

在项目根目录运行，使用 Go **1.20.14**（保留 Windows Server 2012 兼容性）：

```bash
go run ./tools/build
# 或 Linux/macOS: ./build.sh all
# 或 Windows: build.bat all
```

新版 Go 可先设置 `GOTOOLCHAIN=go1.20.14`，工具链会在首次运行时自动下载：

```bash
GOTOOLCHAIN=go1.20.14 ./build.sh all
```

Windows cmd 中使用 `set GOTOOLCHAIN=go1.20.14`，然后运行 `build.bat all`。

默认产物写入 `dist/`，以 `forward_<系统>_<架构>` 命名，Windows 附加 `.exe`：

| 系统 | 架构 | 运行方式 |
| --- | --- | --- |
| Windows | amd64、386 | GUI / CLI |
| Linux | amd64、386、arm64、armv7 | CLI |
| macOS | amd64、arm64 | CLI |

指定部分目标或输出目录：

```bash
go run ./tools/build -targets linux/amd64,linux/arm64,darwin/arm64 -out dist
go run ./tools/build -targets linux/arm/v6,linux/arm/v7
```

构建不修改项目中的二进制、依赖文件或 Windows 资源；每个 Windows 架构均在临时目录生成对应资源，资源生成器固定为 `github.com/akavel/rsrc@v0.10.2`。首次构建需要联网下载依赖。产物不执行 UPX 压缩，完成后生成本次目标的 `SHA256SUMS`。输出目录中的其他文件会保留。

Windows ARM64 暂未支持。交叉编译成功不代表已完成各目标系统的运行验证；发布前应在对应系统测试，尤其是 Windows Server 2012。

## 使用

### CLI(任意平台)

通过配置文件:

```bash
./forward -config forward.json
```

直接传参数(单条规则):

```bash
./forward -listen 0.0.0.0:19100 -target 192.200.190.41:19100 -proto both
```

参数:

| 参数 | 说明 |
| --- | --- |
| `-config` | 配置文件路径(JSON);Windows 上若不指定且无 `-listen/-target`,则进入 GUI |
| `-listen` | 监听地址,如 `0.0.0.0:19100` 或 `0.0.0.0:10000-10100` |
| `-target` | 目标地址,如 `192.200.190.41:19100` |
| `-proto` | `tcp` / `udp` / `both`(默认 `both`) |

`Ctrl+C` 或 `SIGTERM` 优雅停止。

### GUI(Windows)

不带任何参数双击 `forward.exe`,或:

```bat
forward.exe
```

界面里可以增删改规则、保存为 `forward.json`、一键启停、查看实时会话表与流量速率。

### TCP 带宽测试

两端均需使用包含带宽功能的 Forward。测试的是发送端到监听端的单条 TCP 连接吞吐量，速率单位为 **Mbps**（每秒百万比特）。

在 Windows 的「接口测试」标签页操作：

1. 接收端选择「监听(收包)」，勾选「测带宽 (TCP)」，填写本地监听地址，例如 `0.0.0.0:9999`，点击「开始」。
2. 发送端选择「发送(发包)」，同样勾选「测带宽 (TCP)」，填写接收端地址，设置测速时长（默认 10 秒，范围 1～3600 秒），点击「开始」。
3. 界面每半秒刷新实时速率、平均速率和累计传输量。发送结束后自动释放连接、恢复开始按钮，并显示对端实收字节数与平均速率。监听端继续等待下一次测试。

任意时刻可点击「停止」。交换两端角色可测试反向带宽。开启带宽模式后，发送内容、echo 和循环间隔不参与测试；每个监听端一次接收一个测速连接。

Linux/macOS 或 Windows 命令行也可作为测试对端：

```bash
# 接收端：启动 TCP 带宽监听
./forward -probe listen -addr 0.0.0.0:9999 -proto tcp -bandwidth

# 发送端：替换为接收端 IP，持续发送 10 秒后自动退出
./forward -probe send -addr 192.168.1.10:9999 -proto tcp -bandwidth -duration 10
```

测试转发链路时，将发送端地址改为转发器的监听地址，转发目标指向带宽监听端。测速客户端/服务端与转发器应使用不同的进程；GUI 中转发和接口测试不能同时运行。

最终结果以「对端实收」为准；实时发送量表示本机写入连接的载荷字节。统计不包含协议头，测试结果受链路、单连接 TCP、CPU 和转发性能影响。测试到时会完成当前数据帧并等待结果；无响应时最多等待约 5 秒后报错。该模式使用 Forward 专用协议，不兼容 iperf 或普通 echo 监听端。

## 配置文件

`forward.json` 示例:

```json
{
  "rules": [
    {
      "listen": "192.168.1.9:19100",
      "target": "192.200.190.41:19100",
      "proto": "both",
      "deny": ["203.0.113.0/24"]
    }
  ],
  "udpBuffer": 65536,
  "udpTimeout": 600,
  "defaultPolicy": "allow",
  "allow": [],
  "deny": ["198.51.100.5"]
}
```

字段:

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `rules[].listen` | string | — | 监听地址,支持端口范围 `host:start-end` |
| `rules[].target` | string | — | 目标地址,支持与 `listen` 等长的端口范围 |
| `rules[].proto` | string | `both` | `tcp` / `udp` / `both` |
| `rules[].allow` | []string | — | 规则级白名单(单 IP 或 CIDR) |
| `rules[].deny` | []string | — | 规则级黑名单 |
| `udpBuffer` | int | 65536 | UDP 单包缓冲区大小(字节) |
| `udpTimeout` | int | 600 | UDP 会话空闲超时(秒) |
| `defaultPolicy` | string | `allow` | 全局默认策略:`allow` / `deny` |
| `allow` | []string | — | 全局白名单 |
| `deny` | []string | — | 全局黑名单 |

端口范围规则:
- `listen` 为范围、`target` 为单端口:所有监听端口都转到同一个目标
- `listen` 与 `target` 都是范围:长度必须一致,按下标一一对应
- `listen` 为单端口、`target` 为范围:**不允许**

## 黑/白名单(ACL)

支持单 IP(`192.168.1.10`)和 CIDR(`10.0.0.0/8`、`2001:db8::/32`)。可在全局或规则级配置。

**匹配顺序**(命中即返,不再继续):

1. 客户端 IP 命中 **规则 deny** 或 **全局 deny** → 拒绝
2. 任一级存在非空 `allow` 列表 → 必须命中 `allow` 才放行,否则拒绝
3. 否则按 `defaultPolicy`(`allow` / `deny`,默认 `allow`)

**常见用法**:

- **只拉黑某些 IP**:全局 `defaultPolicy=allow`,把要禁的写进 `deny`
- **只允许内网**:全局 `defaultPolicy=deny`,把内网网段写进 `allow`
- **某条规则单独加白名单**:在该 `rules[].allow` 写允许的 IP,其他规则不受影响

TCP 上 ACL 拒绝时直接 `close` 连接;UDP 上拒绝时丢包不建会话。被拒次数会累加到 `denied` 计数,GUI 状态栏的"拒绝"显示。

## 协议与实现说明

- **TCP**:`net.Listen` 监听后每条连接起两个 goroutine 双向 `Read/Write`,16 KB 缓冲
- **UDP**:主 socket 读包→按客户端地址查/建会话(后端 socket `DialUDP` 到目标)→后端读包回写客户端;主 socket `2 MB`、会话 socket `512 KB` 收发缓冲;空闲到 `udpTimeout` 由定时器清理
- **统计**:全部用 `atomic.Int64`,GUI 端轮询 `snapshotSessions()` 计算速率
- **GC**:启动时 `debug.SetGCPercent(50)`,停止后 `debug.FreeOSMemory()` 立即归还内存
