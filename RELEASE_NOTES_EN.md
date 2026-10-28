# Forward Release Notes

## v1.1.0 · 2026-10-28

### Changes

- Added TCP bandwidth testing to the Windows interface-test tab, with a configurable duration of 1–3600 seconds (10 seconds by default).
- Displays live/average Mbps, transferred bytes, and receiver-confirmed byte counts and throughput.
- Supports testing through a Forward relay. The sender finishes automatically; the listener remains available for subsequent tests.
- Added CLI `-bandwidth` and `-duration` options so Linux/macOS can act as test peers. Both peers must run this version and enable bandwidth mode.
- Improved probe connection cleanup, cancellation of blocked sends, and repeated starts.
- Added a shared cross-platform build tool with architecture-specific Windows resources and SHA256 checksums.

### Release Assets

| Platform | Architectures | Files |
| --- | --- | --- |
| Windows | amd64 / 386 | `forward_windows_amd64.exe` / `forward_windows_386.exe` |
| Linux | amd64 / 386 | `forward_linux_amd64` / `forward_linux_386` |
| Linux | arm64 / armv7 | `forward_linux_arm64` / `forward_linux_armv7` |
| macOS | amd64 / arm64 | `forward_darwin_amd64` / `forward_darwin_arm64` |

Choose the file for your operating system and architecture. On Linux/macOS, grant execute permission before running it. `SHA256SUMS` covers all eight executables.

### Bandwidth Example

```bash
# Receiver
./forward_linux_amd64 -probe listen -addr 0.0.0.0:9999 -proto tcp -bandwidth
# Sender: replace the address with your receiver's address
./forward_linux_amd64 -probe send -addr 192.0.2.10:9999 -proto tcp -bandwidth -duration 10
```

### Validation and Compatibility

- All eight targets are built with Go 1.20.14 to retain Windows Server 2012 compatibility.
- Race-enabled tests and host/Windows amd64 static checks pass.
- Tests cover direct/relayed transfers, repeated starts, blocked-send cancellation, invalid peers, interrupted transfers, and TCP/UDP echo regression.
- macOS ARM64 CLI transfers, live statistics, input validation, and SIGTERM shutdown have been exercised.
- The Windows GUI and other target systems have not been tested on-device. Windows ARM64 is not included. Bandwidth mode uses a Forward-specific TCP protocol and is not compatible with iperf or a regular echo listener.

## Project Overview

Forward is a lightweight TCP/UDP port-forwarding tool written in Go. Windows provides a graphical interface; Linux and macOS provide CLI operation.

### Highlights

- Supports TCP, UDP, and combined TCP/UDP forwarding.
- Supports single ports and port ranges, including indexed range-to-range mapping.
- Maintains UDP sessions by client address and reclaims idle sessions after a timeout.
- Provides global and per-rule ACLs with individual IP and CIDR allow/deny entries.
- Reports connection, session, traffic, rate, and denied-request statistics.
- Windows GUI supports rule editing, configuration saving, start/stop control, and session inspection.
- Includes a `-probe` connectivity test mode with TCP/UDP listen, send, and echo operations.
- Produces a single executable, with optional UPX compression.

### Build Requirements

- Build with Go 1.20.x to retain Windows Server 2012 compatibility.
- Linux/macOS: run `./build.sh`; use `./build.sh upx` to enable compression.
- Windows: run `build.bat` for the GUI executable; run `build.bat linux` to also produce a Linux binary.
- All targets: use Go 1.20.14 with `go run ./tools/build`, `./build.sh all`, or `build.bat all`.

### Usage Examples

```bash
./forward -config forward.json
./forward -listen 0.0.0.0:19100 -target 192.0.2.10:19100 -proto both
./forward -probe listen -addr 0.0.0.0:9999 -proto tcp -echo
```

### Notes

Before deployment, review listener and target addresses together with the `allow`, `deny`, and `defaultPolicy` settings. Run `go test -race ./...` and `go vet ./...`, then use the built-in probe mode to validate your deployed network path.
