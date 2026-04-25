# Development

Contributor-oriented notes: adapter contracts, worktree layout, testing, and CI.

For user-facing command docs and operating workflows, see **[cli.md](cli.md)**.
For install and prerequisites, see **[install.md](install.md)**.
For SDD and Spec Kit behavior, see **[spec-driven.md](spec-driven.md)**.

## Adapter interface

Each adapter is a Bash file in the `agents/` directory that `agentctl` sources via Bash at runtime. An adapter **must** implement two functions:

### `agent_launch(wt, issue, port, session_id, kickoff, headless)`

Starts the coding agent in the worktree `$wt`.

| Parameter | Description |
|-----------|-------------|
| `wt` | Absolute path to the linked worktree |
| `issue` | Issue number (string) |
| `port` | Reserved dev-server port |
| `session_id` | Unique session identifier (UUID) |
| `kickoff` | Multi-line kickoff prompt string |
| `headless` | `1` = background mode, `0` = interactive |

When running headless, the adapter **must** append `agent-pid=<pid>` to `$wt/.agent`.

### `agent_resume(wt, prompt)`

Resumes a paused headless agent with new instructions.

| Parameter | Description |
|-----------|-------------|
| `wt` | Absolute path to the linked worktree |
| `prompt` | Revision feedback string |

### Naming convention

The file must be named `agents/<name>.sh`. It is selected with `agentctl spawn --agent <name>`. Use `agentctl spawn --help` to see flags; available adapters are the `*.sh` files under `agents/`.

### Example skeleton

```bash
#!/usr/bin/env bash
# my-bot adapter for agentctl

agent_launch() {
  local wt="$1" issue="$2" _port="$3" session_id="$4" kickoff="$5" headless="$6"
  cd "$wt" || exit 1
  if (( headless )); then
    nohup my-bot --session "$session_id" < "$kickoff" > agent.log 2>&1 &
    echo "agent-pid=$!" >> .agent
  else
    exec my-bot --session "$session_id" < "$kickoff"
  fi
}

agent_resume() {
  local wt="$1" prompt="$2"
  ( cd "$wt" && nohup my-bot --resume --message "$prompt" >> agent.log 2>&1 & )
}
```

## Adapter notes

### GitHub Copilot CLI (`copilot`)

| Property | Detail |
|----------|--------|
| Binary | `copilot` |
| Install | `npm install -g @github/copilot` |
| Auth | `GITHUB_TOKEN` env var, or run `copilot auth` interactively |
| Non-interactive flag | `-p / --prompt` |
| Session continuity | `--session-id <uuid>` on both launch and resume |
| Headless | Runs safely under `nohup`; does not require a TTY |
| Resume limitation | `--session-id` support depends on the installed CLI version. If the flag is not recognised, resume with `cd <worktree> && copilot` manually. |

## Worktree layout

When `agentctl spawn <issue>` runs, it creates a linked worktree at `../<repo>-<issue>-<slug>/` containing:

```
.agent          ← key=value metadata (agent, port, session-id, agent-pid, dev-pid)
agent.log       ← agent stdout/stderr (headless mode)
specs/          ← SpecKit artefacts (spec.md, plan.md, tasks.md)
```

## Install

See **[install.md](install.md)** for prerequisites, layout, clone/symlink/subtree installs, and release archives.

## Testing strategy

### Unit tests — pure helpers

Pure, deterministic functions (slug conversion, spec-state inference, kickoff building, PID formatting) live in `internal/cmd` and are tested with table-driven tests in `commands_test.go`. No external processes or filesystem side-effects.

### Hermetic integration tests — git helpers

Functions in `internal/git` that shell out to the `git` CLI are tested with real temporary repositories created by `t.TempDir()` + `git init`. Each test is self-contained: it creates a repo, adds worktrees or branches as needed, exercises the helper, and the directory is cleaned up automatically by the test framework. Tests are skipped when `git` is not on `PATH`.

### Process tests

`internal/process` tests use the live OS: `IsAlive` is exercised against the test process's own PID; `Kill` is exercised by spawning a real `sleep` process, terminating it, and waiting for the child to be reaped before asserting liveness.

### What is not tested (and why)

- **Cobra command wiring** (`cmd/agentctl/main.go`): the entry point is a thin dispatch layer; coverage comes from the `internal/cmd` tests above.
- **`runSpawn`, `runCleanupMerged`, `runStatus`**: these call `gh`, `npm`, `lsof`, and `uuidgen`; stub-based integration tests are tracked in [#19](https://github.com/arun-gupta/agentctl/issues/19).
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
- **[shellcheck](../.github/workflows/shellcheck.yml)** — lints the `agents/` Bash adapters with [ShellCheck](https://www.shellcheck.net/)
- **[snapshot](../.github/workflows/snapshot.yml)** — cross-compiles `agentctl` for all supported platforms on every push to `main` and uploads archives as workflow artifacts (14-day retention); see [install.md](install.md#prebuilt-binaries----per-commit-snapshots)
- **[release](../.github/workflows/release.yml)** — builds and attaches release archives when a `v*` tag is pushed

```bash
# Run locally
go build ./...
go test -cover ./...
go vet ./...
shellcheck agents/claude.sh agents/copilot.sh agents/codex.sh agents/gemini.sh agents/opencode.sh
```
