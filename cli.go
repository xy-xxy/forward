// cli.go - 入口 + CLI 模式
//
// 用法：
//   forward[.exe]                                # 无参数：GUI 模式（仅 Windows）
//   forward[.exe] -config forward.json           # CLI 模式
//   forward[.exe] -listen 0.0.0.0:19100 -target 192.200.190.41:19100 -proto both

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
)

func main() {
	// 内存调优：GC 更勤一点，常驻堆更小
	debug.SetGCPercent(50)

	// Windows 上把 stdout 接到父 cmd（如果有），非 Windows 上是空操作
	attachParentConsole()

	configFile := flag.String("config", "", "配置文件路径（json）；Windows 上不指定则进入 GUI 模式")
	listenAddr := flag.String("listen", "", "监听地址，如 0.0.0.0:19100（CLI 模式）")
	targetAddr := flag.String("target", "", "目标地址，如 192.200.190.41:19100（CLI 模式）")
	proto := flag.String("proto", "both", "协议: tcp / udp / both（CLI 模式）")
	flag.Parse()

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

	logger := log.New(os.Stdout, "", log.LstdFlags)
	fwd := NewForwarder(func(s string) { logger.Println(s) })

	if err := fwd.Start(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}

	// 等待 Ctrl+C / kill 信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	logger.Println("收到中断信号，正在停止...")
	fwd.Stop()
	logger.Println("已退出")
}
