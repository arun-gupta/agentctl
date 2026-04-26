# Development

Contributor-oriented notes: adapter contracts, worktree layout, testing, and CI.

For user-facing command docs and operating workflows, see **[cli.md](cli.md)**.
For install and prerequisites, see **[install.md](install.md)**.
For SDD and Spec Kit behavior, see **[spec-driven.md](spec-driven.md)**.
For the SDD methodology YAML schema, lookup hierarchy, and drop-in locations, see **[sdd.md](sdd.md)**.
For the YAML adapter schema, lookup hierarchy, and drop-in locations, see **[adapters.md](adapters.md)**.

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
specs/          ← SpecKit artefacts (spec.md, plan.md, tasks.md)
```

## Install

See **[install.md](install.md)** for prerequisites, layout, clone/symlink/subtree installs, and release archives.

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

### Running with coverage

```bash
go test -cover ./...
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out
```

CI uploads `coverage.out` as an artifact on every run (see `.github/workflows/go.yml`).

## CI

This repository runs several GitHub Actions workflows on every push and pull request:

- **[go](../.github/workflows/go.yml)** — `go build ./...`, `go test -cover ./...`, `go vet ./...`
- **[snapshot](../.github/workflows/snapshot.yml)** — cross-compiles `agentctl` for all supported platforms on every push to `main` and uploads archives as workflow artifacts (14-day retention); see [install.md](install.md#prebuilt-binaries----per-commit-snapshots)
- **[release](../.github/workflows/release.yml)** — builds and attaches release archives when a `v*` tag is pushed

```bash
# Run locally
go build ./...
go test -cover ./...
go vet ./...
```
