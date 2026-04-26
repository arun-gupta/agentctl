# AGENTS.md — agentctl contributor conventions

Guidance for AI coding agents (and humans using them) working in this repository.

## Workflow

- **No direct commits.** All changes must go through a pull request.
- **Test-Driven Development.** Write a failing test before writing implementation code.
- **Report test results.** After every `go test ./...` run, include the full output in your response.
- **Report ShellCheck results.** After any change to `agents/*.sh`, run `shellcheck agents/*.sh` and include the output.

## Project shape

```
cmd/agentctl/     ← Go CLI entry point (cobra); one file: main.go
internal/
  cmd/            ← subcommand implementations + tests
  git/            ← git worktree operations + tests
  process/        ← process management + tests
  state/          ← .agent metadata read/write + tests
agents/
  claude.sh       ← Claude Code adapter
  codex.sh        ← OpenAI Codex CLI adapter
  copilot.sh      ← GitHub Copilot CLI adapter
docs/
  cli.md          ← command reference and workflows
  development.md  ← build, test, release, adapter contract, worktree layout, CI
```

**The `agentctl` binary must live next to the `agents/` directory.** The executable's directory is used at runtime to resolve adapter paths; `go build -o agentctl ./cmd/agentctl` from the repo root satisfies this.

## Build & test

```bash
go build ./...                        # verify compilation
go build -o agentctl ./cmd/agentctl   # produce the binary
go test ./...                         # unit tests
go vet ./...                          # static analysis
shellcheck agents/claude.sh agents/codex.sh agents/copilot.sh
```

CI runs all of the above on every push and pull request (`.github/workflows/go.yml`, `.github/workflows/shellcheck.yml`). Fix the root cause of any failure; do not suppress ShellCheck warnings with inline directives.

## Constraints

- **No direct commits.** PRs only (see Workflow above).
- **Adapter interface is stable.** Each `agents/<name>.sh` must export exactly `agent_launch` and `agent_resume`. Signatures are in `docs/development.md`. Do not rename or remove parameters.
- **Co-location contract.** Do not move `agentctl` or `agents/` independently; adapter resolution is path-based.
- **Spec artefact paths.** Pause-state logic depends on `specs/<issue>-*/spec.md`, `plan.md`, `tasks.md`. Changing these paths requires updating all three adapters and `internal/state` in the same PR.
- **No new top-level commands** without a corresponding issue and discussion.
- **No new external Go dependencies** without explicit justification; add via `go get`.
- **No new adapter stubs** unless the agent CLI has a stable non-interactive launch mechanism (see [#16](https://github.com/arun-gupta/agentctl/issues/16)).
- **Do not bump the Go toolchain version** incidentally while fixing something else.

## Merging

- **Never invoke `gh pr merge` directly.** Open the PR and ask the user to merge it manually.

## PR hygiene

- One logical change per PR; no unrelated cleanups.
- `gofmt`-formatted Go, ShellCheck-clean Bash, consistent flag and variable naming.
- Conventional commit prefixes: `feat:`, `fix:`, `docs:`, `refactor:`, `chore:`.
- PR titles ≤ 72 characters.

## Security & secrets

- Never commit tokens, API keys, or credentials.
- Adapters must not hard-code credentials; rely on environment variables (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, etc.).
- Document any new required environment variable in `docs/development.md`.
