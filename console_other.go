//go:build !windows

package main

// attachParentConsole - 非 Windows 平台上 stdout 本来就是终端，无需特殊处理
func attachParentConsole() {}
