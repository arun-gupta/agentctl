# Development

Contributor-oriented notes: full CLI reference, workflows, adapters, layout, install variants, and CI.

## Spec-driven development and SpecKit

The default `agentctl spawn` workflow is **SDD with a human checkpoint**: the agent runs **Stage 1** (write a spec), stops for your approval or revision (`approve-spec` / `revise-spec` when headless), then **Stage 2** (plan, tasks, implement) and opens a PR. That flow is implemented in terms of [Spec Kit](https://github.com/github/spec-kit)—the kickoff tells the agent to use `/speckit.specify`, `/speckit.plan`, `/speckit.tasks`, and `/speckit.implement`, and `agentctl` infers pause state from files under `specs/` (for example `spec.md` vs `plan.md` / `tasks.md`).

**agentctl does not install or vendor Spec Kit.** The **target repository** (and your agent setup, e.g. Claude Code slash commands) must already support that SpecKit-style lifecycle. If your repo is not set up for it, use **`--no-speckit`** on `spawn` so the agent skips that lifecycle and works straight toward a PR with no spec-review pause.

## Usage

```
Provision an isolated agent worktree for an issue and launch a coding agent in it.

Usage:
  agentctl spawn [--agent <name>] [--headless] [--no-speckit] <issue-number> [slug]
  agentctl approve-spec        <issue-number>
  agentctl revise-spec         <issue-number> <feedback>
  agentctl discard             [<issue-number>]
  agentctl cleanup-merged      [<issue-number>]
  agentctl cleanup-all-merged
  agentctl status

Flags (spawn):
  --agent <name>         Select coding agent adapter (default: claude).
  --headless             Run agent in background (log -> agent.log)
  --no-speckit           Skip SpecKit lifecycle; agent opens a PR directly (no spec pause)

Subcommands:
  approve-spec           Release the spec-review pause for a paused headless spawn
  revise-spec            Send non-empty revision feedback to a paused spawn
  discard                Discard worktree + delete local/remote branch (unrecoverable; prompts for confirmation)
  cleanup-merged         Post-merge: pull main, remove worktree, delete local+remote branch
  cleanup-all-merged     Batch sweep: run cleanup-merged on every worktree whose PR is MERGED
  status                 Compact table: issue, branch, agent, port, spec state, PR state.
  status --verbose       Full table: adds PATH, DEV-PID, AGENT-PID, SESSION.
  -h, --help             Show help and exit
```

### Batch workflow

```bash
# Spawn three issues in parallel (headless)
for i in 210 211 212; do agentctl spawn --headless "$i"; done

# Approve all specs once you've reviewed them
for i in 210 211 212; do agentctl approve-spec "$i"; done

# Sweep up everything that's been merged
agentctl cleanup-all-merged

# Check the status of all active worktrees
agentctl status
```

## Adapter interface

Each adapter is a Bash file in the `agents/` directory that `agentctl` sources via Bash at runtime. An adapter **must** implement three functions:

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

### `agent_pause_state(wt, issue)` → stdout

Returns a single-word state string based on the presence of spec artefacts in `$wt/specs/`:

| Return value | Meaning |
|-------------|---------|
| `no-spec` | No spec directory found |
| `paused` | `spec.md` exists but no `plan.md` or `tasks.md` |
| `in-progress` | `plan.md` exists but no `tasks.md` |
| `done` | `tasks.md` exists |

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

agent_pause_state() {
  local wt="$1" issue="$2"
  local state="no-spec"
  if [[ -n "${issue:-}" ]] && compgen -G "$wt/specs/${issue}-*/spec.md" > /dev/null 2>&1; then
    if compgen -G "$wt/specs/${issue}-*/tasks.md" > /dev/null 2>&1; then
      state="done"
    elif compgen -G "$wt/specs/${issue}-*/plan.md" > /dev/null 2>&1; then
      state="in-progress"
    else
      state="paused"
    fi
  fi
  echo "$state"
}
```

## Worktree layout

When `agentctl spawn <issue>` runs, it creates a linked worktree at `../<repo>-<issue>-<slug>/` containing:

```
.agent          ← key=value metadata (agent, port, session-id, agent-pid, dev-pid)
agent.log       ← agent stdout/stderr (headless mode)
specs/          ← SpecKit artefacts (spec.md, plan.md, tasks.md)
```

## Install instructions

### Option A — Go binary from clone (recommended)

```bash
git clone https://github.com/arun-gupta/agentctl
cd agentctl
go build -o agentctl ./cmd/agentctl
# Keep agentctl and agents/ in the same directory (see README).
```

### Option B — symlink the built binary (agents stay in the clone)

```bash
git clone https://github.com/arun-gupta/agentctl ~/.local/share/agentctl
cd ~/.local/share/agentctl && go build -o agentctl ./cmd/agentctl
ln -sf ~/.local/share/agentctl/agentctl ~/.local/bin/agentctl
# Adapters resolve from the real path of the agentctl binary (~/.local/share/agentctl/agents/).
```

### Option C — git subtree

```bash
git subtree add --prefix agentctl \
  https://github.com/arun-gupta/agentctl main --squash
```

Then `cd agentctl && go build -o agentctl ./cmd/agentctl` (or use a **GitHub Release** archive that already contains `agentctl` + `agents/`).

## CI

This repository runs several GitHub Actions workflows on every push and pull request:

- **[go](../.github/workflows/go.yml)** — `go build ./...`, `go test ./...`, `go vet ./...`
- **[shellcheck](../.github/workflows/shellcheck.yml)** — lints the `agents/` Bash adapters with [ShellCheck](https://www.shellcheck.net/)
- **[snapshot](../.github/workflows/snapshot.yml)** — cross-compiles `agentctl` for all supported platforms on every push to `main` and uploads archives as workflow artifacts (14-day retention); see [install.md](install.md#prebuilt-binaries----per-commit-snapshots)
- **[release](../.github/workflows/release.yml)** — builds and attaches release archives when a `v*` tag is pushed

```bash
# Run ShellCheck locally
shellcheck agents/claude.sh agents/copilot.sh agents/codex.sh
```
