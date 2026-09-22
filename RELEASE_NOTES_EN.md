# Forward Release Notes

## Current Development Version

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

### Usage Examples

```bash
./forward -config forward.json
./forward -listen 0.0.0.0:19100 -target 192.0.2.10:19100 -proto both
./forward -probe listen -addr 0.0.0.0:9999 -proto tcp -echo
```

### Notes

Before deployment, review listener and target addresses together with the `allow`, `deny`, and `defaultPolicy` settings. The repository currently has no standalone unit-test suite; validate releases with `go vet ./...` and the built-in probe mode.
