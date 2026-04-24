# agentctl

**agentctl** is a shell toolkit for provisioning isolated [git worktrees](https://git-scm.com/docs/git-worktree) per GitHub issue and launching a coding agent inside each one.  It supports multiple agent back-ends via a simple adapter interface.

Migrated from [arun-gupta/repo-pulse](https://github.com/arun-gupta/repo-pulse) with full commit history preserved.

---

## Repository layout

```
agent.sh          ← main entry-point (spawn, approve-spec, revise-spec, …)
agents/
  claude.sh       ← Claude Code adapter
  copilot.sh      ← GitHub Copilot adapter (stub — not yet implemented)
```

---

## Prerequisites

| Tool | Purpose |
|------|---------|
| `git` ≥ 2.5 | worktree support |
| `gh` CLI | PR management (`--cleanup-merged`, `--status`) |
| `claude` CLI | required when using the `claude` adapter |

---

## Quick start

```bash
# Clone agentctl alongside your project repo
git clone https://github.com/arun-gupta/agentctl
# Symlink (or copy) agent.sh into your project's scripts directory
ln -s /path/to/agentctl/agent.sh scripts/agent.sh

# Spawn a worktree for issue #42 and open Claude interactively
./agent.sh 42

# Run headless (background) with a custom slug
./agent.sh --headless --agent claude 42 my-feature

# Approve the spec and resume the agent
./agent.sh --approve-spec 42

# Clean up after the PR is merged
./agent.sh --cleanup-merged 42
```

---

## Usage

```
Provision an isolated agent worktree for an issue and launch a coding agent in it.

Usage:
  agent.sh [--agent <name>] [--headless] [--no-speckit] <issue-number> [slug]
  agent.sh --approve-spec        <issue-number>
  agent.sh --revise-spec         <issue-number> <feedback>
  agent.sh --discard             [<issue-number>]
  agent.sh --cleanup-merged      [<issue-number>]
  agent.sh --cleanup-all-merged
  agent.sh --status

Options:
  --agent <name>         Select coding agent adapter (default: claude).
  --headless             Run agent in background (log -> agent.log)
  --no-speckit           Skip SpecKit lifecycle; agent opens a PR directly (no spec pause)
  --approve-spec         Release the spec-review pause for a paused headless spawn
  --revise-spec          Send non-empty revision feedback to a paused spawn
  --discard              Discard worktree + delete local/remote branch (unrecoverable; prompts for confirmation)
  --cleanup-merged       Post-merge: pull main, remove worktree, delete local+remote branch
  --cleanup-all-merged   Batch sweep: run --cleanup-merged on every worktree whose PR is MERGED
  --status, --list       Compact table: issue, branch, agent, port, spec state, PR state.
  --status --verbose     Full table: adds PATH, DEV-PID, AGENT-PID, SESSION.
  -h, --help             Show this help and exit
```

### Batch workflow

```bash
# Spawn three issues in parallel (headless)
for i in 210 211 212; do agent.sh --headless "$i"; done

# Approve all specs once you've reviewed them
for i in 210 211 212; do agent.sh --approve-spec "$i"; done

# Sweep up everything that's been merged
agent.sh --cleanup-all-merged

# Check the status of all active worktrees
agent.sh --status
```

---

## Adapter interface

Each adapter is a Bash file in the `agents/` directory that sources into `agent.sh` at runtime.  An adapter **must** implement three functions:

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

The file must be named `agents/<name>.sh`.  It is selected with `--agent <name>`.  Use `agent.sh --help` to list all available adapters.

### Example skeleton

```bash
#!/usr/bin/env bash
# my-bot adapter for agent.sh

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

---

## Worktree layout

When `agent.sh <issue>` runs, it creates a linked worktree at `../<repo>-<issue>-<slug>/` containing:

```
.agent          ← key=value metadata (agent, port, session-id, agent-pid, dev-pid)
agent.log       ← agent stdout/stderr (headless mode)
specs/          ← SpecKit artefacts (spec.md, plan.md, tasks.md)
```

---

## Install instructions

### Option A — symlink into an existing repo

```bash
git clone https://github.com/arun-gupta/agentctl ~/.local/share/agentctl
ln -s ~/.local/share/agentctl/agent.sh /path/to/your/repo/scripts/agent.sh
# The agents/ directory is resolved relative to agent.sh automatically.
```

### Option B — git subtree

```bash
git subtree add --prefix agentctl \
  https://github.com/arun-gupta/agentctl main --squash
```

### Option C — curl install (single-file, no history)

```bash
curl -fsSL https://raw.githubusercontent.com/arun-gupta/agentctl/main/agent.sh \
  -o scripts/agent.sh
mkdir -p scripts/agents
curl -fsSL https://raw.githubusercontent.com/arun-gupta/agentctl/main/agents/claude.sh \
  -o scripts/agents/claude.sh
chmod +x scripts/agent.sh scripts/agents/claude.sh
```

---

## CI

This repository runs [ShellCheck](https://www.shellcheck.net/) on every push and pull request via GitHub Actions.

```bash
# Run locally
shellcheck agent.sh agents/claude.sh agents/copilot.sh
```

---

## License

See [LICENSE](LICENSE).
