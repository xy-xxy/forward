#!/usr/bin/env bash
# Forward build script (Linux / macOS)
# 用法:
#   ./build.sh            # 编译当前平台
#   ./build.sh upx        # 编译 + UPX 压缩（需要先 apt install upx 或 yum install upx）

set -euo pipefail

echo "[1/3] Go version:"
go version
echo

echo "[2/3] go mod tidy"
go mod tidy

echo "[3/3] go build"
# Linux 不需要 -H windowsgui；rsrc_windows.syso 通过文件名自动被排除
go build -trimpath -ldflags="-s -w" -o forward .

if [ "${1:-}" = "upx" ]; then
    if command -v upx >/dev/null 2>&1; then
        echo
        echo "[+] UPX compression"
        upx --best --lzma -q forward
    else
        echo "WARN: upx not found, skipping compression"
        echo "      Ubuntu/Debian: sudo apt install upx"
        echo "      RHEL/CentOS:   sudo yum install upx"
    fi
fi

echo
echo "Done: $(pwd)/forward ($(stat -c%s forward 2>/dev/null || stat -f%z forward) bytes)"
