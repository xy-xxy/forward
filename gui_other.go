//go:build !windows

package main

import (
	"flag"
	"fmt"
	"os"
)

// runGUI - 非 Windows 平台无 GUI，提示并退出
func runGUI() {
	fmt.Fprintln(os.Stderr, "本程序在非 Windows 平台仅支持 CLI 模式。")
	fmt.Fprintln(os.Stderr, "用法:")
	fmt.Fprintln(os.Stderr, "  forward -config forward.json")
	fmt.Fprintln(os.Stderr, "  forward -listen 0.0.0.0:19100 -target 192.200.190.41:19100 -proto both")
	fmt.Fprintln(os.Stderr, "")
	flag.Usage()
	os.Exit(2)
}
