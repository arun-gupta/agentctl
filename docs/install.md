# Install

How to install **agentctl** and what you need on your machine.

## Prerequisites

### Required

| Requirement | Purpose |
|-------------|---------|
| `git` ≥ 2.5 | worktree support |
| `gh` CLI | PR management (`cleanup-merged`, `status`), slug-from-title |

### Required for the default workflow

| Requirement | Purpose |
|-------------|---------|
| `claude` CLI | default coding-agent adapter |
| Spec Kit in the **target app repo** | default SDD flow; see [sdd.md](sdd.md). Use `start --no-sdd` if not set up |

### Optional

| Requirement | Purpose |
|-------------|---------|
| `codex` CLI | required only when using `--agent codex` (`npm install -g @openai/codex`) |
| `gemini` CLI | required only when using `--agent gemini` (`npm install -g @google/gemini-cli`); auth via `GEMINI_API_KEY` |
| `opencode` CLI | required only when using `--agent opencode` (`npm install -g opencode@latest`); configure auth via `opencode auth` or set your provider API key |
| `copilot` CLI | required only when using `--agent copilot`; install from [GitHub Copilot CLI](https://github.com/features/copilot/cli) (`npm install -g @github/copilot`); auth via `GITHUB_TOKEN` or `copilot auth` |
| Go | only to build from source (see `go.mod`) |

## Homebrew (macOS and Linux) — recommended

```bash
brew install arun-gupta/tap/agentctl
```

To add the tap once and install later:

```bash
brew tap arun-gupta/tap
brew install agentctl
```

Tap source: [github.com/arun-gupta/homebrew-tap](https://github.com/arun-gupta/homebrew-tap)

## Prebuilt binaries — GitHub Releases

Tagged releases publish archives for all supported platforms. Download the archive for your OS/arch, extract it, and move the binary onto your `PATH`.

**macOS / Linux**

```bash
# Replace <os>-<arch> with your platform:
# linux-amd64 | linux-arm64 | darwin-amd64 | darwin-arm64
curl -fsSL https://github.com/arun-gupta/agentctl/releases/latest/download/agentctl-<os>-<arch>.tar.gz \
  | tar -xz
sudo mv agentctl /usr/local/bin/agentctl
agentctl --version
```

**Windows (PowerShell)**

```powershell
# Download from the Releases page:
# https://github.com/arun-gupta/agentctl/releases/latest
Expand-Archive agentctl-windows-amd64.zip -DestinationPath .
.\agentctl.exe --version
```

Each release also ships a `checksums.txt` with SHA256 digests for every archive. Verify before installing:

```bash
sha256sum -c checksums.txt --ignore-missing
```

## Prebuilt binaries — per-commit snapshots

Every push to `main` runs the [`snapshot` workflow](../.github/workflows/snapshot.yml) which publishes
workflow artifacts for the full platform matrix (14-day retention). Use these to test unreleased builds.

1. Go to **[Actions → snapshot](https://github.com/arun-gupta/agentctl/actions/workflows/snapshot.yml)**.
2. Open the latest successful run on `main`.
3. Download the artifact for your platform, e.g. `agentctl-<sha>-linux-amd64` (`.tar.gz`) or `agentctl-<sha>-windows-amd64` (`.zip`).
4. Extract and move `agentctl` (or `agentctl.exe`) onto your `PATH`.

Artifact naming: `agentctl-<7-char-sha>-<goos>-<goarch>`, e.g. `agentctl-a1b2c3d-linux-amd64.tar.gz`.

## Install from source

```bash
git clone https://github.com/arun-gupta/agentctl
cd agentctl
go build -o agentctl ./cmd/agentctl
sudo mv agentctl /usr/local/bin/agentctl
agentctl --version
```

For contributor builds, test commands, and cross-compilation see **[development.md](development.md)**.

## Provenance

Migrated from [arun-gupta/repo-pulse](https://github.com/arun-gupta/repo-pulse) with preserved history.
