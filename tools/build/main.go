// 跨平台构建入口：go run ./tools/build；产物默认写入 dist。
package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const defaultTargets = "windows/amd64,windows/386,linux/amd64,linux/386,linux/arm64,linux/arm/v7,darwin/amd64,darwin/arm64"

type target struct{ os, arch, arm string }

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "构建失败:", err)
		os.Exit(1)
	}
}

func run() error {
	targetsFlag := flag.String("targets", defaultTargets, "逗号分隔的平台/架构；ARM 可指定 linux/arm/v7")
	outFlag := flag.String("out", "dist", "输出目录")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("不支持的位置参数: %v", flag.Args())
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, "forward.manifest")); err != nil {
		return fmt.Errorf("请从 forward 项目根目录运行: %w", err)
	}
	out, err := filepath.Abs(*outFlag)
	if err != nil {
		return err
	}
	// 先校验所有目标，避免参数错误时留下部分产物。
	supported, err := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "tool", "dist", "list").Output()
	if err != nil {
		return err
	}
	var targets []target
	seen := map[string]bool{}
	windows := false
	for _, name := range strings.Split(*targetsFlag, ",") {
		parts := strings.Split(strings.TrimSpace(name), "/")
		if len(parts) < 2 || len(parts) > 3 {
			return fmt.Errorf("无效目标: %q", name)
		}
		t := target{os: parts[0], arch: parts[1]}
		if len(parts) == 3 {
			if t.arch != "arm" || (parts[2] != "v5" && parts[2] != "v6" && parts[2] != "v7") {
				return fmt.Errorf("无效 ARM 版本: %q", name)
			}
			t.arm = strings.TrimPrefix(parts[2], "v")
		} else if t.arch == "arm" {
			t.arm = "7"
		}
		if !strings.Contains("\n"+string(supported), "\n"+t.os+"/"+t.arch+"\n") {
			return fmt.Errorf("Go 不支持目标: %q", name)
		}
		if t.os != "windows" && t.os != "linux" && t.os != "darwin" {
			return fmt.Errorf("尚未支持的系统: %s", t.os)
		}
		if t.os == "windows" {
			if t.arch != "amd64" && t.arch != "386" {
				return fmt.Errorf("Windows GUI 目前仅支持 amd64/386")
			}
			windows = true
		}
		key := t.os + "/" + t.arch + "/" + t.arm
		if seen[key] {
			return fmt.Errorf("重复目标: %q", name)
		}
		seen[key] = true
		targets = append(targets, t)
	}
	version, err := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "version").Output()
	if err != nil {
		return err
	}
	fmt.Print(string(version))
	if windows && !strings.Contains(string(version), " go1.20.") {
		return fmt.Errorf("Windows Server 2012 构建需要 Go 1.20.x；请使用 Go 1.20.14，或在新版 Go 下设置 GOTOOLCHAIN=go1.20.14")
	}
	stage, err := os.MkdirTemp("", "forward-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !(strings.HasSuffix(name, ".go") || name == "go.mod" || name == "go.sum" || name == "forward.ico" || name == "forward.manifest") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stage, name), data, 0644); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(out, 0755); err != nil {
		return err
	}
	var sums strings.Builder
	for _, t := range targets {
		label := t.os + "_" + t.arch
		if t.arm != "" {
			label += "v" + t.arm
		}
		name := "forward_" + label
		if t.os == "windows" {
			name += ".exe"
		}
		fmt.Println("构建", label)
		resource := filepath.Join(stage, "rsrc_windows.syso")
		if t.os == "windows" {
			// 为每个 Windows 架构重新生成资源，避免链接仓库中的 amd64 COFF。
			if err := command(stage, hostEnv(), "run", "github.com/akavel/rsrc@v0.10.2", "-arch", t.arch, "-manifest", "forward.manifest", "-ico", "forward.ico", "-o", resource); err != nil {
				return err
			}
		}
		ldflags := "-s -w"
		if t.os == "windows" {
			ldflags += " -H windowsgui"
		}
		env := append(hostEnv(), "GOOS="+t.os, "GOARCH="+t.arch, "GOARM="+t.arm, "GOAMD64=v1", "GO386=sse2", "CGO_ENABLED=0")
		path := filepath.Join(out, name)
		if err := command(stage, env, "build", "-mod=readonly", "-trimpath", "-ldflags="+ldflags, "-o", path, "."); err != nil {
			return err
		}
		if t.os == "windows" {
			if err := os.Remove(resource); err != nil {
				return err
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(data), name)
	}
	if err := os.WriteFile(filepath.Join(out, "SHA256SUMS"), []byte(sums.String()), 0644); err != nil {
		return err
	}
	fmt.Println("构建完成:", out)
	return nil
}

// 构建工具在宿主机执行，不继承外部交叉编译变量。
func hostEnv() []string {
	var env []string
	for _, item := range os.Environ() {
		key := strings.SplitN(item, "=", 2)[0]
		switch key {
		case "GOOS", "GOARCH", "GOARM", "GOAMD64", "GO386", "CGO_ENABLED":
			continue
		}
		env = append(env, item)
	}
	return append(env, "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
}

func command(dir string, env []string, args ...string) error {
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
