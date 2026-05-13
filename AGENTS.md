# AGENTS.md — agentctl contributor conventions

Guidance for AI coding agents (and humans using them) working in this repository.

## Workflow

- **No direct commits.** All changes must go through a pull request.
- **Test-Driven Development.** Write a failing test before writing implementation code.
- **Report test results.** After every `go test ./...` run, include the full output in your response.

## Project shape

```
cmd/agentctl/     ← Go CLI entry point (cobra); one file: main.go
internal/
  adapters/       ← YAML adapter loader, built-in YAMLs, tests
  cmd/            ← subcommand implementations + tests
  config/         ← .agentctl.yml schema, read/write helpers
  git/            ← git worktree operations + tests
  notify/         ← desktop notification helpers (macOS osascript, Linux notify-send)
  process/        ← process management + tests
  sdd/            ← SDD methodology loader, built-in YAMLs, tests
  state/          ← .agent metadata read/write + tests
  vcs/            ← VCS provider abstraction (GitHub, GitLab); detect, auth, PR ops
  xdg/            ← XDG base-directory helpers for config/data paths
docs/
  cli.md          ← command reference and workflows
  adapters.md     ← adapter YAML schema and built-in adapter reference
  sdd.md          ← SDD overview, methodology schema, and drop-in locations
  development.md  ← build, test, release, adapter contract, worktree layout, CI
```

## Build & test

```bash
go build ./...                        # verify compilation
go build -o agentctl ./cmd/agentctl   # produce the binary
go test ./...                         # unit tests
go vet ./...                          # static analysis
```

CI runs all of the above on every push and pull request (`.github/workflows/go.yml`). ShellCheck runs automatically against any `.sh` files found in the repo (`.github/workflows/shellcheck.yml`). Fix the root cause of any failure.

## Constraints

- **No direct commits.** PRs only (see Workflow above).
- **Adapter YAML schema is stable.** The fields `binary`, `launch`, `resume_cmd`, `session_type`, `prompt`, `session`, `resume_id`, and `install` are the public contract. Do not rename or remove fields without a migration path.
- **Spec artefact paths.** Pause-state logic depends on `specs/<issue>-*/spec.md`, `plan.md`, `tasks.md`. Changing these paths requires updating all affected code and `internal/state` in the same PR.
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
