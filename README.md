# agentctl

**agentctl** is a shell toolkit for provisioning isolated [git worktrees](https://git-scm.com/docs/git-worktree) per GitHub issue and launching a coding agent inside each one. It supports multiple agent back-ends via a simple adapter interface. By default it follows **spec-driven development (SDD)**: a spec is produced and reviewed before the agent carries out the full implementation plan.

Migrated from [arun-gupta/repo-pulse](https://github.com/arun-gupta/repo-pulse) with full commit history preserved.

## Spec-driven development and SpecKit

Today’s default workflow is **SDD with a human checkpoint**: the agent runs **Stage 1** (write a spec), stops for your approval or revision (`--approve-spec` / `--revise-spec` when headless), then **Stage 2** (plan, tasks, implement) and opens a PR. That flow is **implemented in terms of [SpecKit](https://github.com/github/spec-kit)**—the kickoff tells the agent to use `/speckit.specify`, `/speckit.plan`, `/speckit.tasks`, and `/speckit.implement`, and `agent.sh` infers pause state from files under `specs/` (for example `spec.md` vs `plan.md` / `tasks.md`).

**agentctl does not install or vendor SpecKit.** The **target repository** (and your agent setup, e.g. Claude Code slash commands) must already support that SpecKit-style lifecycle. If your repo is not set up for it, use **`--no-speckit`** so the agent skips that lifecycle and works straight toward a PR with no spec-review pause.

## Repository layout

```
agent.sh          ← main entry-point (spawn, approve-spec, revise-spec, …)
agents/
  claude.sh       ← Claude Code adapter
  codex.sh        ← OpenAI Codex CLI adapter
  copilot.sh      ← GitHub Copilot adapter (stub — not yet implemented)
```

## Prerequisites

| Requirement | Purpose |
|-------------|---------|
| `git` ≥ 2.5 | worktree support |
| `gh` CLI | PR management (`--cleanup-merged`, `--status`) |
| `claude` CLI | required when using the `claude` adapter (default) |
| `codex` CLI | required when using the `codex` adapter (`npm install -g @openai/codex`) |
| SpecKit (or equivalent) in the **target repo** | Needed for the default SDD flow (see above). `agent.sh` does not install or verify it. Use `--no-speckit` when the repo is not set up for that workflow. |
| GitHub Copilot CLI (`gh copilot`) | optional; intended for the `copilot` adapter (stub until non-interactive launch/resume exists) |

## Installation

### From a Git clone (recommended)

`agent.sh` resolves the `agents/` directory relative to itself, so install from a checkout (or keep the full tree if you copy files).

```bash
git clone https://github.com/arun-gupta/agentctl
ln -s /path/to/agentctl/agent.sh /path/to/your/repo/scripts/agent.sh
```

Use `scripts/agent.sh` (or add it to your `PATH`) in the examples below.

### With curl (scripts only)

If you only want the scripts in an existing repo:

```bash
mkdir -p scripts/agents
curl -fsSL https://raw.githubusercontent.com/arun-gupta/agentctl/main/agent.sh -o scripts/agent.sh
curl -fsSL https://raw.githubusercontent.com/arun-gupta/agentctl/main/agents/claude.sh -o scripts/agents/claude.sh
chmod +x scripts/agent.sh scripts/agents/claude.sh
```

For **git subtree**, vendoring paths, and local ShellCheck, see **[docs/development.md](docs/development.md)**.

### Prebuilt binaries (per-commit snapshots)

CI publishing is tracked in **[#13](https://github.com/arun-gupta/agentctl/issues/13)**. Once that lands, each successful push to `main` will expose downloadable artifacts (script bundle first; native `agentctl` binaries when the Go port ships in [#10](https://github.com/arun-gupta/agentctl/issues/10)).

## Quick start

```bash
# Spawn a worktree for issue #42 and open Claude interactively
./agent.sh 42

# Run headless (background) with a custom slug
./agent.sh --headless --agent claude 42 my-feature

# Approve the spec and resume the agent
./agent.sh --approve-spec 42

# Clean up after the PR is merged
./agent.sh --cleanup-merged 42
```

Run `./agent.sh --help` for the full option list.

For batch workflows, the adapter contract, worktree layout, alternate install methods (subtree, curl), and local ShellCheck, see **[docs/development.md](docs/development.md)**.

## License

See [LICENSE](LICENSE).
