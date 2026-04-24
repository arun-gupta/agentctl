# agentctl

**agentctl** is a **Go** CLI for provisioning isolated [git worktrees](https://git-scm.com/docs/git-worktree) per GitHub issue and launching a coding agent inside each one. It supports multiple agent back-ends via Bash adapter scripts under `agents/` (sourced at runtime). By default it follows **spec-driven development (SDD)**: a spec is produced and reviewed before the agent carries out the full implementation plan.

Migrated from [arun-gupta/repo-pulse](https://github.com/arun-gupta/repo-pulse) with full commit history preserved. See [AGENTS.md](AGENTS.md) for AI agent and contributor conventions.

## Spec-driven development and SpecKit

By default `agentctl spawn` follows **spec-driven development (SDD)**: Stage 1 writes a spec and pauses for human review; Stage 2 implements and opens a PR. This is built on [Spec Kit](https://github.com/github/spec-kit), which must already be set up in the **target repository**. Use `--no-speckit` to skip the spec-review pause. See [docs/development.md](docs/development.md#spec-driven-development-and-speckit) for full details.

## Repository layout

```
cmd/agentctl/     ← Go CLI (cobra)
internal/         ← git, process, state, commands
agents/
  claude.sh       ← Claude Code adapter
  codex.sh        ← OpenAI Codex CLI adapter
  copilot.sh      ← GitHub Copilot adapter (stub — not yet implemented)
  gemini.sh       ← Google Gemini CLI adapter
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
| `gemini` CLI | required when using the `gemini` adapter (`npm install -g @google/gemini-cli`); auth via `GEMINI_API_KEY` |
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

Tagged [GitHub Releases](https://github.com/arun-gupta/agentctl/releases/latest) publish archives for
`linux-amd64`, `linux-arm64`, `darwin-amd64`, `darwin-arm64`, and `windows-amd64`. Every push to `main`
also produces per-commit snapshot artifacts (14-day retention) via the
[`snapshot` workflow](https://github.com/arun-gupta/agentctl/actions/workflows/snapshot.yml).

For platform-specific download commands, Windows notes, and snapshot artifact details, see
**[docs/install.md](docs/install.md)**.

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
