# agentctl

**agentctl** combines [git worktrees](https://git-scm.com/docs/git-worktree), coding agents, and optional spec-driven development (SDD) to tackle GitHub issues in isolated workspaces.

It is a **Go** CLI with pluggable **coding agents** and **SDD methodologies**.

| Coding agents | SDD methodologies |
|---|---|
| <img src="https://cdn.simpleicons.org/anthropic/000000" alt="" width="16" height="16"> **Claude Code** (default)<br><img src="https://upload.wikimedia.org/wikipedia/commons/4/4d/OpenAI_Logo.svg" alt="" width="16" height="16"> **OpenAI Codex**<br><img src="https://cdn.simpleicons.org/googlegemini/8E75B2" alt="" width="16" height="16"> **Gemini CLI**<br><img src="https://opencode.ai/favicon.svg" alt="" width="16" height="16"> **OpenCode**<br><img src="https://cdn.simpleicons.org/githubcopilot/000000" alt="" width="16" height="16"> **GitHub Copilot CLI** (stub) | **Spec Kit** (default, supported)<br>**OpenSpec** (planned: [#38](https://github.com/arun-gupta/agentctl/issues/38))<br>**AgentOS** (planned: [#35](https://github.com/arun-gupta/agentctl/issues/35))<br>**specs.md** (planned: [#36](https://github.com/arun-gupta/agentctl/issues/36))<br>**Kiro-style specs** (planned: [#39](https://github.com/arun-gupta/agentctl/issues/39)) |

## Install

**Stable release** (recommended) — download the archive for your platform, extract it, and add the `agentctl/` directory to your `PATH`:

```bash
# macOS (Apple Silicon)
curl -fsSL https://github.com/arun-gupta/agentctl/releases/latest/download/agentctl-darwin-arm64.tar.gz | tar -xz
export PATH="$(pwd)/agentctl:$PATH"
```

For other platforms, download the matching archive from the [Releases page](https://github.com/arun-gupta/agentctl/releases).

**Build from source:**

```bash
git clone https://github.com/arun-gupta/agentctl && cd agentctl
go build -o agentctl ./cmd/agentctl
export PATH="$(pwd):$PATH"   # keep agentctl next to ./agents/
```

See **[docs/install.md](docs/install.md)** for full details, symlink setup, and subtree installs.

## Quick start

Run commands from your **application** repository (the primary git worktree):

```bash
cd /path/to/your/app-repo
agentctl spawn 42
agentctl approve-spec 42       # headless: after you review the spec
agentctl cleanup-merged 42
```

`agentctl --help` and `agentctl <command> --help` list all flags.

## Documentation

- **[docs/install.md](docs/install.md)** — prerequisites, layout, install paths, releases  
- **[docs/cli.md](docs/cli.md)** — command reference and workflows  
- **[docs/spec-driven.md](docs/spec-driven.md)** — SDD, Spec Kit, `--no-speckit`  
- **[docs/development.md](docs/development.md)** — adapters, testing, CI  
- **[docs/build.md](docs/build.md)** — contributor build, test, coverage  
- **[AGENTS.md](AGENTS.md)** — conventions for AI agents working in this repo  

## License

See [LICENSE](LICENSE).
