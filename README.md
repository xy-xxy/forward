# Forward

一个轻量的 TCP / UDP 端口转发工具,使用 Go 编写,跨平台。Windows 下提供基于 [lxn/walk](https://github.com/lxn/walk) 的图形界面,Linux/macOS 下作为 CLI 守护进程运行。

## 特性

- **TCP / UDP / 双协议** 转发,单条规则可同时监听 TCP 与 UDP
- **端口范围** 支持,例如 `0.0.0.0:10000-10100 -> 1.2.3.4:10000-10100`
- **UDP 会话保持**,按客户端地址聚合,空闲超时自动回收
- **实时统计**:连接数、UDP 会话数、字节数、速率,GUI 下还能看到每条会话的详情
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

## 配置文件

`forward.json` 示例:

```json
{
  "rules": [
    {
      "listen": "192.168.1.9:19100",
      "target": "192.200.190.41:19100",
      "proto": "both"
    }
  ],
  "udpBuffer": 65536,
  "udpTimeout": 600
}
```

字段:

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `rules[].listen` | string | — | 监听地址,支持端口范围 `host:start-end` |
| `rules[].target` | string | — | 目标地址,支持与 `listen` 等长的端口范围 |
| `rules[].proto` | string | `both` | `tcp` / `udp` / `both` |
| `udpBuffer` | int | 65536 | UDP 单包缓冲区大小(字节) |
| `udpTimeout` | int | 600 | UDP 会话空闲超时(秒) |

端口范围规则:
- `listen` 为范围、`target` 为单端口:所有监听端口都转到同一个目标
- `listen` 与 `target` 都是范围:长度必须一致,按下标一一对应
- `listen` 为单端口、`target` 为范围:**不允许**

## 协议与实现说明

- **TCP**:`net.Listen` 监听后每条连接起两个 goroutine 双向 `Read/Write`,16 KB 缓冲
- **UDP**:主 socket 读包→按客户端地址查/建会话(后端 socket `DialUDP` 到目标)→后端读包回写客户端;主 socket `2 MB`、会话 socket `512 KB` 收发缓冲;空闲到 `udpTimeout` 由定时器清理
- **统计**:全部用 `atomic.Int64`,GUI 端轮询 `snapshotSessions()` 计算速率
- **GC**:启动时 `debug.SetGCPercent(50)`,停止后 `debug.FreeOSMemory()` 立即归还内存
