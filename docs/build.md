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

Push a `v*` tag to publish a new release:

```bash
git tag v0.2.0
git push origin v0.2.0
```

This triggers a chain of three automated workflows:

```
tag push
  └─▶ release workflow        — builds archives for all platforms, publishes GitHub Release
        └─▶ bump-homebrew      — opens a PR in homebrew-tap with updated version + SHA256s
              └─▶ tap CI       — brew audit + brew install smoke test on the bump PR
```

**release workflow** ([`.github/workflows/release.yml`](../.github/workflows/release.yml))
- Builds `agentctl` for Linux/macOS/Windows × amd64/arm64
- Publishes archives (`agentctl-<os>-<arch>.tar.gz` / `.zip`) as release assets
- Generates `checksums.txt` (SHA256 per archive) and includes it in the release

**bump-homebrew workflow** ([`.github/workflows/bump-homebrew.yml`](../.github/workflows/bump-homebrew.yml))
- Triggered automatically when the GitHub Release is published
- Downloads `checksums.txt` from the new release
- Patches `version` and `sha256` values in `Formula/agentctl.rb` in [homebrew-tap](https://github.com/arun-gupta/homebrew-tap)
- Opens a PR (`bump/agentctl-vX.Y.Z`) in homebrew-tap for review
- Requires the `HOMEBREW_TAP_TOKEN` secret (fine-grained PAT scoped to homebrew-tap with contents + pull-requests write)

**tap CI** ([homebrew-tap `.github/workflows/ci.yml`](https://github.com/arun-gupta/homebrew-tap/blob/main/.github/workflows/ci.yml))
- Runs `brew audit --strict agentctl` on every push and PR
- Runs `brew install arun-gupta/tap/agentctl` and `agentctl --version` smoke test

Merge the bump PR in homebrew-tap once tap CI is green. Use `v<major>.<minor>.0` for a new release (e.g. `v0.2.0`, `v0.3.0`).

## CI

Every push and pull request runs the following via the [`go` workflow](../.github/workflows/go.yml):

```bash
go build ./...
go test ./...
go vet ./...
```
