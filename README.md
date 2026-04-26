# agentctl

**agentctl** is a **Go** CLI that combines [git worktrees](https://git-scm.com/docs/git-worktree), coding agents, and optional spec-driven development (SDD) methodologies to tackle GitHub issues in isolated workspaces, with pluggability across both agents and SDD workflows.

| Coding agents | SDD methodologies |
|---|---|
| [![Claude Code](https://img.shields.io/badge/Claude%20Code-1F1F1F?logo=anthropic&logoColor=white)](https://www.anthropic.com/claude-code) (default)<br>[![OpenAI Codex](https://img.shields.io/badge/OpenAI%20Codex-412991?logo=openai&logoColor=white)](https://github.com/openai/codex)<br>[![Gemini CLI](https://img.shields.io/badge/Gemini%20CLI-8E75B2?logo=googlegemini&logoColor=white)](https://github.com/google-gemini/gemini-cli)<br>…and [more](docs/adapters.md) | [**Spec Kit**](https://github.com/github/spec-kit) (`--sdd speckit`)<br>[**Plain**](docs/sdd.md) (`--sdd plain`)<br>…and [any methodology](docs/sdd.md) via a one-line YAML file |

## Install

**Homebrew** (macOS and Linux):

```bash
brew install arun-gupta/tap/agentctl
```

**Manual** — download a release archive, extract it, and move the binary onto your `PATH`:

```bash
# macOS (Apple Silicon)
curl -fsSL https://github.com/arun-gupta/agentctl/releases/latest/download/agentctl-darwin-arm64.tar.gz | tar -xz
sudo mv agentctl /usr/local/bin/
```

For other platforms, source builds, and subtree installs, see **[docs/install.md](docs/install.md)**.

## Upgrade

**Homebrew:**

```bash
brew upgrade agentctl
```

**Manual** — re-run the same `curl` command from Install to replace the binary.

## Quick start

Run commands from your **application** repository (the primary git worktree):

```bash
cd /path/to/your/app-repo
agentctl start 42
agentctl cleanup-merged 42
```

`agentctl --help` and `agentctl <command> --help` list all flags.

## Documentation

- **[docs/install.md](docs/install.md)** — prerequisites, install paths, releases  
- **[docs/cli.md](docs/cli.md)** — command reference and workflows  
- **[docs/sdd.md](docs/sdd.md)** — SDD overview, methodology YAML schema, resolution chain, drop-in locations  
- **[docs/adapters.md](docs/adapters.md)** — agent YAML schema, lookup hierarchy, drop-in locations, built-in adapters  
- **[docs/development.md](docs/development.md)** — contributor build, test, release, adapter contracts, CI  
- **[AGENTS.md](AGENTS.md)** — conventions for AI agents working in this repo  

## License

See [LICENSE](LICENSE).
