# Install and layout

How to install **agentctl**, what you need on your machine, and how files are arranged.

## Repository layout

```
cmd/agentctl/     ← Go CLI (cobra)
internal/         ← git, process, state, commands
agents/
  claude.sh       ← Claude Code adapter
  codex.sh        ← OpenAI Codex CLI adapter
  copilot.sh      ← GitHub Copilot adapter (stub)
```

The **`agentctl` binary must live in the same directory as the `agents/` folder** (the executable’s directory is used to resolve adapter paths). Building from a clone at repo root keeps `./agentctl` next to `./agents/`.

## Prerequisites

| Requirement | Purpose |
|-------------|---------|
| `git` ≥ 2.5 | worktree support |
| `bash` | adapters are sourced and run via Bash |
| `gh` CLI | PR management (`cleanup-merged`, `status`), slug-from-title |
| `claude` CLI | required for the `claude` adapter (default) |
| `codex` CLI | required for the `codex` adapter (`npm install -g @openai/codex`) |
| Spec Kit in the **target app repo** | default SDD flow; see [spec-driven.md](spec-driven.md). Use `spawn --no-speckit` if not set up |
| `gh copilot` | optional; for the `copilot` adapter (stub) |
| Go | only to build from source (see `go.mod`) |

## Install from source (clone)

```bash
git clone https://github.com/arun-gupta/agentctl
cd agentctl
go build -o agentctl ./cmd/agentctl
# Run ./agentctl from this directory, or add this directory to PATH
./agentctl --help
```

To install elsewhere, copy **`agentctl` and `agents/`** into the same directory (for example `/opt/agentctl/`) and put that directory on your `PATH`.

### Symlink only the binary

Agents resolve from the **real path** of the executable:

```bash
git clone https://github.com/arun-gupta/agentctl ~/.local/share/agentctl
cd ~/.local/share/agentctl && go build -o agentctl ./cmd/agentctl
ln -sf ~/.local/share/agentctl/agentctl ~/.local/bin/agentctl
# Adapters: ~/.local/share/agentctl/agents/
```

### Git subtree

```bash
git subtree add --prefix agentctl \
  https://github.com/arun-gupta/agentctl main --squash
```

Then `cd agentctl && go build -o agentctl ./cmd/agentctl`, or unpack a **GitHub Release** archive that already contains `agentctl` + `agents/`.

## Prebuilt binaries

- Per-commit / release automation: [#13](https://github.com/arun-gupta/agentctl/issues/13)
- Homebrew: [#14](https://github.com/arun-gupta/agentctl/issues/14)

Tagged **GitHub Releases** ship archives with **`agentctl` plus `agents/`**; extract the folder and add it to your `PATH`.

## Contributor builds

For `go build ./...`, tests, and coverage, see **[build.md](build.md)**.

## Provenance

Migrated from [arun-gupta/repo-pulse](https://github.com/arun-gupta/repo-pulse) with preserved history.
