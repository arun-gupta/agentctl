# agentctl

**agentctl** is a **Go** CLI that combines [git worktrees](https://git-scm.com/docs/git-worktree), coding agents, and optional spec-driven development (SDD) methodologies to tackle GitHub issues in isolated workspaces, with pluggability across both agents and SDD workflows.

| Coding agents | SDD methodologies |
|---|---|
| [![Claude Code](https://img.shields.io/badge/Claude%20Code-1F1F1F?logo=anthropic&logoColor=white)](https://www.anthropic.com/claude-code) (default)<br>[![OpenAI Codex](https://img.shields.io/badge/OpenAI%20Codex-412991?logo=openai&logoColor=white)](https://github.com/openai/codex)<br>[![Gemini CLI](https://img.shields.io/badge/Gemini%20CLI-8E75B2?logo=googlegemini&logoColor=white)](https://github.com/google-gemini/gemini-cli)<br>[![OpenCode](https://img.shields.io/badge/OpenCode-111111?logo=terminal&logoColor=white)](https://opencode.ai/)<br>[![GitHub Copilot CLI](https://img.shields.io/badge/GitHub%20Copilot%20CLI-8957E5?logo=githubcopilot&logoColor=white)](https://github.com/features/copilot/cli)<br>…and any agent supported via a one-line YAML file | [**Spec Kit**](https://github.com/github/spec-kit) (default)<br>[OpenSpec](https://openspec.dev/) ([#38](https://github.com/arun-gupta/agentctl/issues/38))<br>[AgentOS](https://buildermethods.com/agent-os) ([#35](https://github.com/arun-gupta/agentctl/issues/35))<br>[specs.md](https://specs.md/) ([#36](https://github.com/arun-gupta/agentctl/issues/36))<br>[Kiro-style specs](https://kiro.dev/docs/specs/) ([#39](https://github.com/arun-gupta/agentctl/issues/39)) |

agentctl is **fully extensible** — any coding agent can be added by dropping a single YAML file in a config directory. No Go knowledge or pull request required. See **[docs/adapters.md](docs/adapters.md)** for the YAML schema and drop-in locations.

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

## Quick start

Run commands from your **application** repository (the primary git worktree):

```bash
cd /path/to/your/app-repo
agentctl start 42
agentctl approve-spec 42       # headless: after you review the spec
agentctl cleanup-merged 42
```

`agentctl --help` and `agentctl <command> --help` list all flags.

## Documentation

- **[docs/install.md](docs/install.md)** — prerequisites, layout, install paths, releases  
- **[docs/cli.md](docs/cli.md)** — command reference and workflows  
- **[docs/spec-driven.md](docs/spec-driven.md)** — SDD, Spec Kit, `--no-sdd`  
- **[docs/adapters.md](docs/adapters.md)** — YAML adapter schema, lookup hierarchy, drop-in locations, built-in adapters  
- **[docs/development.md](docs/development.md)** — testing, CI  
- **[docs/build.md](docs/build.md)** — contributor build, test, coverage  
- **[AGENTS.md](AGENTS.md)** — conventions for AI agents working in this repo  

## License

See [LICENSE](LICENSE).
