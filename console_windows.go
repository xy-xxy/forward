//go:build windows

package main

import (
	"os"
	"syscall"
)

// attachParentConsole - Windows 专用：把当前进程附着到父 cmd 的控制台
// 因为 exe 用 -H windowsgui 编译时默认没有 console，stdout 无处可写。
// AttachConsole(ATTACH_PARENT_PROCESS = -1) 把它接到调用方的 console 上。
func attachParentConsole() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("AttachConsole")
	ret, _, _ := proc.Call(uintptr(^uint32(0))) // ATTACH_PARENT_PROCESS
	if ret == 0 {
		return // 没有父 console（如从 Explorer 启动），忽略
	}
	if h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE); err == nil && h != 0 {
		os.Stdout = os.NewFile(uintptr(h), "stdout")
	}
	if h, err := syscall.GetStdHandle(syscall.STD_ERROR_HANDLE); err == nil && h != 0 {
		os.Stderr = os.NewFile(uintptr(h), "stderr")
	}
	if h, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE); err == nil && h != 0 {
		os.Stdin = os.NewFile(uintptr(h), "stdin")
	}
}
