# agentctl

**agentctl** is a **Go** CLI that creates a [git worktree](https://git-scm.com/docs/git-worktree) per GitHub issue and launches a coding agent there, using Bash adapters in `agents/`.

## Install

**Stable release** (recommended) — download the archive for your platform, extract it, and add the `agentctl/` directory to your `PATH`:

```bash
# macOS (Apple Silicon)
curl -fsSL https://github.com/arun-gupta/agentctl/releases/latest/download/agentctl-darwin-arm64.tar.gz | tar -xz
export PATH="$(pwd)/agentctl:$PATH"
```

Replace `darwin-arm64` with `darwin-amd64`, `linux-amd64`, or `linux-arm64` as needed. Windows users: download `agentctl-windows-amd64.zip` from the [Releases page](https://github.com/arun-gupta/agentctl/releases).

**Latest build** — per-commit snapshot artifacts are published on every push to `main` via [Actions → snapshot](https://github.com/arun-gupta/agentctl/actions/workflows/snapshot.yml) (14-day retention).

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
- **[docs/spec-driven.md](docs/spec-driven.md)** — SDD, Spec Kit, `--no-speckit`  
- **[docs/development.md](docs/development.md)** — CLI reference, batches, adapters, worktrees, CI  
- **[docs/build.md](docs/build.md)** — contributor build, test, coverage  
- **[AGENTS.md](AGENTS.md)** — conventions for AI agents working in this repo  

## License

See [LICENSE](LICENSE).
