# Repository Guidelines

## Project Structure & Module Organization

This repository is a single Go module (`awesomeProject`) with package `main` at the root. `main.go` contains the TCP/UDP forwarding engine and configuration types; `cli.go` handles command-line entry points; `probe.go` provides connectivity tests; and `acl.go` implements allow/deny matching. Windows GUI code is in `gui_windows.go`, with non-Windows stubs in `gui_other.go`; console attachment follows the same `*_windows.go`/`*_other.go` pattern. `tools/genicon/` is a separate generator for `forward.ico`. `forward.json` is the sample configuration, while `forward.manifest` and `rsrc_windows.syso` support the Windows build.

## Build, Test, and Development Commands

Use Go 1.20.x to preserve Windows Server 2012 compatibility.

```bash
./build.sh                 # build the current Unix-like platform
./build.sh upx             # build and compress with UPX when installed
go build -o forward .      # quick local build
go vet ./...                # static checks
```

On Windows, run `build.bat` for the GUI executable or `build.bat linux` to also cross-compile Linux. The script can generate missing icon resources and compress outputs when `upx.exe` is present. Run locally with `./forward -config forward.json` or with `-listen`, `-target`, and `-proto` flags.

## Coding Style & Naming Conventions

Run `gofmt` on changed Go files and keep standard Go naming: exported identifiers use `PascalCase`, local variables use `camelCase`, and constants use descriptive names. Preserve the existing file-purpose header comments and platform build tags. Match the repository’s Chinese comments, log messages, and GUI strings. Keep forwarding lifecycle work tracked through the existing `WaitGroup` and connection tracking helpers.

## Testing Guidelines

There is no unit-test suite currently. Use `go vet ./...`, build both relevant targets, and validate behavior with the built-in probe: run `-probe listen -addr ... -proto tcp|udp -echo` on one side and `-probe send -addr ... -payload ...` on the other, optionally through a forwarder. Check TCP, UDP, ACL, and port-range changes against a representative configuration.

## Commit & Pull Request Guidelines

Existing history uses short, generic subjects (for example, `Changes`), but new commits should use a concise imperative subject that names the behavior changed. PRs should explain the user-visible effect, list validation commands and platforms, link an issue when applicable, and include screenshots for Windows GUI changes. Keep generated binaries and unrelated IDE changes out of feature commits unless the build specifically requires them.

## Security & Configuration Tips

Treat `forward.json` and command-line bind addresses as deployment configuration. Review `allow`, `deny`, and `defaultPolicy` before exposing a listener, and avoid committing real endpoints or sensitive network details.
