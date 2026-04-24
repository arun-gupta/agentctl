# agentctl

**agentctl** is a shell toolkit for provisioning isolated [git worktrees](https://git-scm.com/docs/git-worktree) per GitHub issue and launching a coding agent inside each one. It supports multiple agent back-ends via a simple adapter interface.

Migrated from [arun-gupta/repo-pulse](https://github.com/arun-gupta/repo-pulse) with full commit history preserved.

## Repository layout

```
agent.sh          ← main entry-point (spawn, approve-spec, revise-spec, …)
agents/
  claude.sh       ← Claude Code adapter
  codex.sh        ← OpenAI Codex CLI adapter
  copilot.sh      ← GitHub Copilot adapter (stub — not yet implemented)
```

## Prerequisites

| Tool | Purpose |
|------|---------|
| `git` ≥ 2.5 | worktree support |
| `gh` CLI | PR management (`--cleanup-merged`, `--status`) |
| `claude` CLI | required when using the `claude` adapter |
| `codex` CLI | required when using the `codex` adapter (`npm install -g @openai/codex`) |

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

Run `./agent.sh --help` for the full option list.

For batch workflows, the adapter contract, worktree layout, alternate install methods (subtree, curl), and local ShellCheck, see **[docs/development.md](docs/development.md)**.

## License

See [LICENSE](LICENSE).
