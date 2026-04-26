# Development

Contributor-oriented notes: building, testing, releasing, adapter contracts, and CI.

For user-facing command docs and operating workflows, see **[cli.md](cli.md)**.
For install and prerequisites, see **[install.md](install.md)**.
For the SDD methodology YAML schema, lookup hierarchy, and drop-in locations, see **[sdd.md](sdd.md)**.
For the YAML adapter schema, lookup hierarchy, and drop-in locations, see **[adapters.md](adapters.md)**.

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

## Testing

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

# Verbose output
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

## Releasing

Push a `v*` tag to publish a new release:

```bash
git tag v0.2.0
git push origin v0.2.0
```

This triggers the release workflow which runs three sequential jobs:

```
tag push
  └─▶ release workflow (release.yml)
        ├─▶ build jobs          — builds archives for all platforms
        ├─▶ release job         — publishes GitHub Release with archives + checksums
        └─▶ bump-homebrew job   — opens a PR in homebrew-tap, enables auto-merge
              └─▶ tap CI        — brew audit + brew install; auto-merges on green
```

**release workflow** ([`.github/workflows/release.yml`](../.github/workflows/release.yml))

*`build` jobs* — Builds `agentctl` for Linux/macOS/Windows × amd64/arm64 and uploads archives as artifacts.

*`release` job* — Downloads all build artifacts, generates `checksums.txt` (SHA256 per archive), and publishes the GitHub Release.

*`bump-homebrew` job* — Runs after `release`; downloads `checksums.txt` from the new release, patches `version` and `sha256` values in `Formula/agentctl.rb` in [homebrew-tap](https://github.com/arun-gupta/homebrew-tap), opens a PR (`bump/agentctl-vX.Y.Z`), and enables auto-merge so it merges automatically once tap CI passes.

> **Note:** The `bump-homebrew` job runs in the same workflow as `release` to avoid GitHub's restriction that prevents workflows triggered by `GITHUB_TOKEN` from cascading to other workflows via repository events.

Requires the `HOMEBREW_TAP_TOKEN` secret — a fine-grained PAT scoped to the homebrew-tap repository with **contents** and **pull-requests** write permission. Set it in **Settings → Secrets and variables → Actions** of this repository.

Use `v<major>.<minor>.0` for a new release (e.g. `v0.2.0`, `v0.3.0`).

## CI

Every push and pull request runs the following via the [`go` workflow](../.github/workflows/go.yml):

```bash
go build ./...
go test -cover -coverprofile=coverage.out ./...
go vet ./...
```

CI uploads `coverage.out` as an artifact on every run (see `.github/workflows/go.yml`).

The [`snapshot` workflow](../.github/workflows/snapshot.yml) cross-compiles `agentctl` for all supported platforms on every push to `main` and uploads archives as workflow artifacts (14-day retention). See [install.md](install.md#prebuilt-binaries----per-commit-snapshots) for how to use them.

## Adapter interface

Adapters are YAML files that describe how to launch and resume a coding agent. See **[adapters.md](adapters.md)** for the full schema and examples.

### Adding a new adapter

Drop a YAML file in one of the lookup locations:

1. **Project-local**: `.agentctl/adapters/<name>.yml` (relative to cwd)
2. **User-level**: `~/.config/agentctl/adapters/<name>.yml`
3. **Built-in** (PR required): `internal/adapters/builtin/<name>.yml`

The adapter name is the filename stem. Select it with `agentctl start --agent <name>`.

### Minimum viable adapter

```yaml
binary: my-bot
```

This gives `my-bot -p {kickoff}` on launch and `my-bot -p {prompt}` on resume.

### Full example

```yaml
binary: my-bot
launch: my-bot --init {kickoff} --id {session_id}
resume_cmd: my-bot --continue {prompt} --id {session_id}
```

## Worktree layout

When `agentctl start <issue>` runs, it creates a linked worktree at `../<repo>-<issue>-<slug>/` containing:

```
.agent          ← key=value metadata (agent, port, session-id, agent-pid, dev-pid)
agent.log       ← agent stdout/stderr (headless mode)
specs/          ← SDD artefacts (spec.md, plan.md, tasks.md)
```

## Testing strategy

### Unit tests — pure helpers

Pure, deterministic functions (slug conversion, spec-state inference, kickoff building, PID formatting) live in `internal/cmd` and are tested with table-driven tests in `commands_test.go`. No external processes or filesystem side-effects. The `internal/sdd` package has its own unit tests covering YAML loading, the resolution chain, and prompt substitution.

### Hermetic integration tests — git helpers

Functions in `internal/git` that shell out to the `git` CLI are tested with real temporary repositories created by `t.TempDir()` + `git init`. Each test is self-contained: it creates a repo, adds worktrees or branches as needed, exercises the helper, and the directory is cleaned up automatically by the test framework. Tests are skipped when `git` is not on `PATH`.

### Process tests

`internal/process` tests use the live OS: `IsAlive` is exercised against the test process's own PID; `Kill` is exercised by spawning a real `sleep` process, terminating it, and waiting for the child to be reaped before asserting liveness.

### What is not tested (and why)

- **Cobra command wiring** (`cmd/agentctl/main.go`): the entry point is a thin dispatch layer; coverage comes from the `internal/cmd` tests above.
- **`runStart`, `runCleanupMerged`, `runStatus`**: these call `gh`, `npm`, `lsof`, and `uuidgen`; stub-based integration tests are tracked in [#19](https://github.com/arun-gupta/agentctl/issues/19).
- **`ghPRState`, `slugFromIssue`**: require a real `gh` authentication context; not suitable for CI without credentials.
