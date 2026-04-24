# agentctl

**agentctl** is a **Go** CLI for provisioning isolated [git worktrees](https://git-scm.com/docs/git-worktree) per GitHub issue and launching a coding agent inside each one. It supports multiple agent back-ends via Bash adapter scripts under `agents/` (sourced at runtime). By default it follows **spec-driven development (SDD)**: a spec is produced and reviewed before the agent carries out the full implementation plan.

Migrated from [arun-gupta/repo-pulse](https://github.com/arun-gupta/repo-pulse) with full commit history preserved. See [AGENTS.md](AGENTS.md) for AI agent and contributor conventions.

## Spec-driven development and SpecKit

Today’s default workflow is **SDD with a human checkpoint**: the agent runs **Stage 1** (write a spec), stops for your approval or revision (`approve-spec` / `revise-spec` when headless), then **Stage 2** (plan, tasks, implement) and opens a PR. That flow is **implemented in terms of [Spec Kit](https://github.com/github/spec-kit)**—the kickoff tells the agent to use `/speckit.specify`, `/speckit.plan`, `/speckit.tasks`, and `/speckit.implement`, and `agentctl` infers pause state from files under `specs/` (for example `spec.md` vs `plan.md` / `tasks.md`).

**agentctl does not install or vendor Spec Kit.** The **target repository** (and your agent setup, e.g. Claude Code slash commands) must already support that SpecKit-style lifecycle. If your repo is not set up for it, use **`--no-speckit`** on `spawn` so the agent skips that lifecycle and works straight toward a PR with no spec-review pause.

## Repository layout

```
cmd/agentctl/     ← Go CLI (cobra)
internal/         ← git, process, state, commands
agents/
  claude.sh       ← Claude Code adapter
  codex.sh        ← OpenAI Codex CLI adapter
  copilot.sh      ← GitHub Copilot adapter (stub — not yet implemented)
```

The **`agentctl` binary must live in the same directory as the `agents/` folder** (the executable’s directory is used to resolve adapter paths). A normal clone + `go build` from repo root satisfies that.

## Prerequisites

| Requirement | Purpose |
|-------------|---------|
| `git` ≥ 2.5 | worktree support |
| `bash` | agent adapters are sourced and run via Bash |
| `gh` CLI | PR management (`cleanup-merged`, `status`), slug-from-title |
| `claude` CLI | required when using the `claude` adapter (default) |
| `codex` CLI | required when using the `codex` adapter (`npm install -g @openai/codex`) |
| SpecKit (or equivalent) in the **target repo** | Needed for the default SDD flow (see above). `agentctl` does not install or verify it. Use `--no-speckit` when the repo is not set up for that workflow. |
| GitHub Copilot CLI (`gh copilot`) | optional; intended for the `copilot` adapter (stub until non-interactive launch/resume exists) |
| Go | only if you build from source (see `go.mod` for the toolchain version) |

## Installation

### Build from clone (recommended)

```bash
git clone https://github.com/arun-gupta/agentctl
cd agentctl
go build -o agentctl ./cmd/agentctl
# Run from this directory so ./agents/ sits next to ./agentctl
./agentctl --help
```

To install elsewhere, keep **`agentctl` and `agents/` in the same directory** (for example copy both into `/opt/agentctl/` and put that directory on your `PATH`, or run from the clone as above).

### Prebuilt binaries

Publishing **Go** snapshot binaries per commit and documenting install from **GitHub Releases** is tracked in **[#13](https://github.com/arun-gupta/agentctl/issues/13)**. Homebrew is tracked in **[#14](https://github.com/arun-gupta/agentctl/issues/14)**.

Tagged **GitHub Releases** attach archives that contain **`agentctl` plus `agents/`**; extract and add that folder to your `PATH`. Vendoring the repo (e.g. git subtree) is described in **[docs/development.md](docs/development.md)**.

## Quick start

Run these from your **application** repository (the primary worktree), with `agentctl` on your `PATH` or invoked by full path.

```bash
# Spawn a worktree for issue #42 and open Claude interactively
agentctl spawn 42

# Run headless (background) with a custom slug
agentctl spawn --headless --agent claude 42 my-feature

# Approve the spec and resume the agent
agentctl approve-spec 42

# Clean up after the PR is merged
agentctl cleanup-merged 42
```

Run `agentctl --help` and `agentctl <command> --help` for options.

For batch workflows, the adapter contract, worktree layout, and local ShellCheck, see **[docs/development.md](docs/development.md)**.

## License

See [LICENSE](LICENSE).
