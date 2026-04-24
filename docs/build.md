# Building agentctl

Instructions for building, testing, and installing the Go CLI.

## Prerequisites

| Requirement | Version |
|-------------|---------|
| [Go](https://go.dev/dl/) | ≥ 1.24 (see `go.mod`) |
| `git` | any recent version |

## Build

```bash
# Build all packages (outputs nothing on success)
go build ./...

# Build the agentctl binary into the current directory
go build -o agentctl ./cmd/agentctl
```

## Run tests

```bash
# All packages
go test ./...

# Single package
go test ./internal/git/...
go test ./internal/process/...
go test ./internal/cmd/...
go test ./internal/state/...

# Single test by name (supports regex)
go test ./internal/git/... -run TestAddRemoveWorktree
go test ./internal/process/... -run TestKill

# Verbose output (shows each test name and result)
go test -v ./...

# With coverage percentages
go test -cover ./...

# Coverage breakdown per function
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

# Open coverage in browser
go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out
```

The hermetic git tests (`internal/git`) create temporary repositories using `t.TempDir()` and require `git` on your `PATH`. They are skipped automatically if `git` is not available.

## Vet

```bash
go vet ./...
```

## Install locally

```bash
# Installs agentctl into $GOBIN (default: $GOPATH/bin or ~/go/bin)
go install ./cmd/agentctl
```

Make sure `$GOBIN` (or `~/go/bin`) is on your `$PATH`:

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
agentctl --help
```

## Cross-compile

Set `GOOS` and `GOARCH` to target a different platform. Use `CGO_ENABLED=0` for a fully static binary.

```bash
# Linux (amd64)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/agentctl-linux-amd64 ./cmd/agentctl

# macOS (Apple Silicon)
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o dist/agentctl-darwin-arm64 ./cmd/agentctl

# Windows (amd64)
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/agentctl-windows-amd64.exe ./cmd/agentctl
```

The full release matrix (Linux/macOS/Windows × amd64/arm64) is built automatically by the
[`release` workflow](../.github/workflows/release.yml) when a `v*` tag is pushed.

## Release build flags

The release workflow stamps the version string into the binary at link time:

```bash
go build -trimpath -ldflags="-s -w -X main.version=<tag>" -o dist/agentctl ./cmd/agentctl
```

| Flag | Effect |
|------|--------|
| `-trimpath` | Remove local file-system paths from the binary |
| `-s -w` | Strip debug symbols and DWARF info (smaller binary) |
| `-X main.version=<tag>` | Embed the Git tag as the version string |

## Releasing

To publish a new release and trigger the binary build workflow, create and push a `v*` tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

This triggers the [`release` workflow](../.github/workflows/release.yml), which builds archives for all supported platforms and publishes them as a GitHub Release. Use `v<major>.<minor>.0` for a new release (e.g. `v0.1.0`, `v0.2.0`).

## CI

Every push and pull request runs the following via the [`go` workflow](../.github/workflows/go.yml):

```bash
go build ./...
go test ./...
go vet ./...
```
