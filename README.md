# agentctl

**agentctl** is a **Go** CLI that creates a [git worktree](https://git-scm.com/docs/git-worktree) per GitHub issue and launches a coding agent there, using Bash adapters in `agents/`.

## Quick start

Build from a clone, then run commands from your **application** repository (the primary git worktree):

```bash
git clone https://github.com/arun-gupta/agentctl && cd agentctl
go build -o agentctl ./cmd/agentctl
export PATH="$(pwd):$PATH"   # keep agentctl next to ./agents/

cd /path/to/your/app-repo
agentctl spawn 42
agentctl approve-spec 42       # headless: after you review the spec
agentctl cleanup-merged 42
```

`agentctl --help` and `agentctl <command> --help` list all flags.

## Documentation

- **[docs/install.md](docs/install.md)** — prerequisites, layout, install paths, releases  
- **[docs/spec-driven.md](docs/spec-driven.md)** — SDD, Spec Kit, `--no-speckit`  
- **[docs/development.md](docs/development.md)** — CLI reference, batches, adapters, worktrees, CI  
- **[docs/build.md](docs/build.md)** — contributor build, test, coverage  
- **[AGENTS.md](AGENTS.md)** — conventions for AI agents working in this repo  

## License

See [LICENSE](LICENSE).
