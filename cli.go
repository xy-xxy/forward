// cli.go - 入口 + CLI 模式
//
// 三种用法:
//   forward                          # GUI 模式(仅 Windows)
//   forward -config forward.json     # 转发模式:按配置文件启动
//   forward -listen ... -target ...  # 转发模式:命令行单条规则
//   forward -probe listen|send -addr ... [-proto tcp|udp] [-payload ...]   # 接口测试

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"
)

// openCLILog 打开当前工作目录下的 forward.log(追加模式)。
// 失败返回 nil,调用方继续仅写 stdout。
func openCLILog() (*os.File, error) {
	const logFileName = "forward.log"
	f, err := os.OpenFile(logFileName, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(f, "\n========== Forward 启动 %s ==========\n", time.Now().Format(time.RFC3339))
	return f, nil
}

// newCLILogger 返回一个同时打到 stdout 和 forward.log 的 logger,以及关闭文件的回调。
func newCLILogger() (*log.Logger, func()) {
	var w io.Writer = os.Stdout
	closeFn := func() {}
	if f, err := openCLILog(); err == nil {
		w = io.MultiWriter(os.Stdout, f)
		closeFn = func() { f.Close() }
		fmt.Fprintln(os.Stderr, "日志同时写入: forward.log")
	} else {
		fmt.Fprintf(os.Stderr, "无法打开 forward.log: %v(仅输出到屏幕)\n", err)
	}
	return log.New(w, "", log.LstdFlags), closeFn
}

const helpText = `Forward — TCP/UDP 端口转发 & 接口测试工具

用法:
  forward                                            GUI 模式(仅 Windows)
  forward -config <file>                             按配置文件启动转发
  forward -listen <addr> -target <addr> [-proto X]   命令行单条转发规则
  forward -probe listen|send -addr <addr> [...]      接口连通性测试(收/发)

转发相关参数:
  -config <file>         配置文件路径(JSON);见下方配置示例
  -listen <ip:port>      监听地址,如 0.0.0.0:19100 或 0.0.0.0:10000-10100
  -target <ip:port>      目标地址,可与 -listen 同长度的端口范围
  -proto tcp|udp|both    协议(默认 both)

接口测试参数:
  -probe listen|send     测试模式:监听(收包)或发送(主动发)
  -addr <ip:port>        监听模式:本地地址 0.0.0.0:9999
                         发送模式:目标地址 1.2.3.4:9999
  -proto tcp|udp         测试协议(默认 tcp)
  -payload <string>      发送模式:要发送的字符串(默认 "ping")
  -echo                  监听模式:收到包后回 echo
  -interval <seconds>    发送模式:循环间隔(默认 0 = 单次)
  -timeout <seconds>     发送模式:等待响应的最长时间(默认 1)

通用:
  -h, -help              显示这段帮助

配置文件示例 (forward.json):
  {
    "rules": [
      { "listen": "0.0.0.0:19100",
        "target": "1.2.3.4:19100",
        "proto":  "both",
        "deny":   ["198.51.100.5"] }
    ],
    "udpBuffer": 65536,
    "udpTimeout": 600,
    "defaultPolicy": "allow",
    "allow": [],
    "deny":  ["203.0.113.0/24"]
  }

示例:
  # 启动转发,允许端口范围
  forward -listen 0.0.0.0:10000-10100 -target 10.0.0.1:10000-10100

  # A 机:监听 9999/TCP 并 echo,验证收包
  forward -probe listen -addr 0.0.0.0:9999 -echo

  # B 机:每秒往 A 机 9999 发一次 "hello"
  forward -probe send -addr A机IP:9999 -payload hello -interval 1

  # 同上,但中间穿过 forward 转发:
  #   B 机 -> forward(监听) -> A 机(监听 echo) -> 看 B 机有没有收到响应
`

func printHelp() {
	fmt.Fprint(os.Stderr, helpText)
}

func main() {
	debug.SetGCPercent(50)
	attachParentConsole()

	// 自定义 Usage,覆盖 flag 默认的简短输出
	flag.Usage = printHelp

	configFile := flag.String("config", "", "配置文件路径")
	listenAddr := flag.String("listen", "", "监听地址")
	targetAddr := flag.String("target", "", "目标地址")
	proto := flag.String("proto", "both", "协议: tcp / udp / both")

	probeMode := flag.String("probe", "", "测试模式: listen / send")
	probeAddr := flag.String("addr", "", "测试地址(监听本地端口或发送目标)")
	probePayload := flag.String("payload", "ping", "发送内容")
	probeEcho := flag.Bool("echo", false, "监听模式:收到后回 echo")
	probeInterval := flag.Int("interval", 0, "发送模式:循环间隔(秒,0=单次)")
	probeTimeout := flag.Int("timeout", 1, "发送模式:等待响应的最长时间(秒)")

	flag.Parse()

	// 接口测试模式
	if *probeMode != "" {
		runProbeCLI(*probeMode, *probeAddr, *proto, *probePayload, *probeEcho, *probeInterval, *probeTimeout)
		return
	}

	cliMode := *configFile != "" || (*listenAddr != "" && *targetAddr != "")
	if cliMode {
		runCLI(*configFile, *listenAddr, *targetAddr, *proto)
		return
	}
	runGUI()
}

func runCLI(configFile, listenAddr, targetAddr, proto string) {
	var cfg Config

	if configFile != "" {
		data, err := os.ReadFile(configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取配置文件失败: %v\n", err)
			os.Exit(1)
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			fmt.Fprintf(os.Stderr, "解析配置文件失败: %v\n", err)
			os.Exit(1)
		}
	} else {
		cfg.Rules = []Rule{{Listen: listenAddr, Target: targetAddr, Proto: proto}}
	}

	logger, closeLog := newCLILogger()
	defer closeLog()
	fwd := NewForwarder(func(s string) { logger.Println(s) })

	if err := fwd.Start(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	logger.Println("收到中断信号，正在停止...")
	fwd.Stop()
	logger.Println("已退出")
}

func runProbeCLI(mode, addr, proto, payload string, echo bool, intervalSec, timeoutSec int) {
	if proto != "tcp" && proto != "udp" {
		proto = "tcp"
	}
	if addr == "" {
		fmt.Fprintln(os.Stderr, "缺少 -addr 参数")
		os.Exit(2)
	}

	var pm ProbeMode
	switch mode {
	case "listen":
		pm = ProbeListen
	case "send":
		pm = ProbeSend
	default:
		fmt.Fprintf(os.Stderr, "-probe 取值必须是 listen 或 send,而不是 %q\n", mode)
		os.Exit(2)
	}

	logger, closeLog := newCLILogger()
	defer closeLog()
	probe := NewProbe(func(s string) { logger.Println(s) })

	cfg := ProbeConfig{
		Mode:        pm,
		Proto:       proto,
		Addr:        addr,
		Payload:     []byte(payload),
		Echo:        echo,
		Interval:    time.Duration(intervalSec) * time.Second,
		ReadTimeout: time.Duration(timeoutSec) * time.Second,
	}
	if err := probe.Start(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}

	// send 模式 + 单次:发完读响应就退出
	if pm == ProbeSend && intervalSec == 0 {
		// 等待响应 + 一点点 buffer
		time.Sleep(time.Duration(timeoutSec)*time.Second + 200*time.Millisecond)
		probe.Stop()
		logger.Printf("发送 %s 字节,接收 %s 字节", humanBytes(probe.Sent()), humanBytes(probe.Recv()))
		return
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	logger.Println("收到中断信号，正在停止...")
	probe.Stop()
	logger.Printf("发送 %s 字节,接收 %s 字节", humanBytes(probe.Sent()), humanBytes(probe.Recv()))
}
