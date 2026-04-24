# Install

How to install **agentctl** and what you need on your machine.

## Prerequisites

### Required

| Requirement | Purpose |
|-------------|---------|
| `git` ≥ 2.5 | worktree support |
| `bash` | adapters are sourced and run via Bash |
| `gh` CLI | PR management (`cleanup-merged`, `status`), slug-from-title |

### Required for the default workflow

| Requirement | Purpose |
|-------------|---------|
| `claude` CLI | default coding-agent adapter |
| Spec Kit in the **target app repo** | default SDD flow; see [spec-driven.md](spec-driven.md). Use `spawn --no-speckit` if not set up |

### Optional

| Requirement | Purpose |
|-------------|---------|
| `codex` CLI | required only when using `--agent codex` (`npm install -g @openai/codex`) |
| `gemini` CLI | required only when using `--agent gemini` (`npm install -g @google/gemini-cli`); auth via `GEMINI_API_KEY` |
| GitHub Copilot CLI | required only when using `--agent copilot` once the adapter is implemented; install from [GitHub Copilot CLI](https://github.com/features/copilot/cli) (`npm install -g @github/copilot` or install script) |
| Go | only to build from source (see `go.mod`) |

## Prebuilt binaries — GitHub Releases (stable)

Tagged releases publish archives for all supported platforms. Download the archive for your OS/arch, extract it, and add the `agentctl/` directory to your `PATH`.

**macOS / Linux**

```bash
# Replace <os>-<arch> with your platform:
# linux-amd64 | linux-arm64 | darwin-amd64 | darwin-arm64
curl -fsSL https://github.com/arun-gupta/agentctl/releases/latest/download/agentctl-<os>-<arch>.tar.gz \
  | tar -xz
sudo mv agentctl /usr/local/bin/agentctl   # or any directory on your PATH
agentctl version
```

**Windows (PowerShell)**

```powershell
# Download from the Releases page:
# https://github.com/arun-gupta/agentctl/releases/latest
# Then extract and move agentctl.exe to a directory on your PATH.
Expand-Archive agentctl-windows-amd64.zip -DestinationPath .
.\agentctl\agentctl.exe version
```

> **Note:** The archive contains both the `agentctl` binary and the `agents/` adapter scripts.  
> Keep both in the same directory (e.g. `/opt/agentctl/`) and add that directory to your `PATH`.

## Prebuilt binaries — per-commit snapshots

Every push to `main` runs the [`snapshot` workflow](../.github/workflows/snapshot.yml) which publishes
workflow artifacts for the full platform matrix (14-day retention). Use these to test unreleased builds.

1. Go to **[Actions → snapshot](https://github.com/arun-gupta/agentctl/actions/workflows/snapshot.yml)**.
2. Open the latest successful run on `main`.
3. Download the artifact for your platform, e.g. `agentctl-<sha>-linux-amd64` (`.tar.gz`) or `agentctl-<sha>-windows-amd64` (`.zip`).
4. Extract and place `agentctl` (or `agentctl.exe`) + the `agents/` directory in the same folder on your `PATH`.

Artifact naming: `agentctl-<7-char-sha>-<goos>-<goarch>`, e.g. `agentctl-a1b2c3d-linux-amd64.tar.gz`.

## Install from source (clone)

Use this path for development, local patches, or when you do not want a prebuilt archive.

```bash
git clone https://github.com/arun-gupta/agentctl
cd agentctl
go build -o agentctl ./cmd/agentctl
# Run ./agentctl from this directory, or add this directory to PATH
./agentctl --help
```

To install elsewhere, keep **`agentctl` and `agents/` in the same directory** (for example copy both into `/opt/agentctl/`) and put that directory on your `PATH`.

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

## Binary and adapter layout

The **`agentctl` binary must live in the same directory as the `agents/` folder** (the executable's directory is used to resolve adapter paths). Release archives already use this layout, and building from a clone at repo root keeps `./agentctl` next to `./agents/`.

```
cmd/agentctl/     ← Go CLI (cobra)
internal/         ← git, process, state, commands
agents/
  claude.sh       ← Claude Code adapter
  codex.sh        ← OpenAI Codex CLI adapter
  copilot.sh      ← GitHub Copilot adapter (stub)
  gemini.sh       ← Google Gemini CLI adapter
```

## Contributor builds

For `go build ./...`, tests, and coverage, see **[build.md](build.md)**.

## Provenance

Migrated from [arun-gupta/repo-pulse](https://github.com/arun-gupta/repo-pulse) with preserved history.
