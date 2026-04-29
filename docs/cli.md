# CLI reference and workflows

Canonical command reference and operating workflows for **agentctl**.

Run `agentctl --help` or `agentctl <command> --help` for generated help from the binary.

## Command reference

### `agentctl start`

```bash
agentctl start [--agent <name>] [--headless] [--notify] [--quiet] <issue-number-or-url> [slug] [--sdd=<name>]
```

Creates a linked worktree for a GitHub issue and launches the selected coding agent inside it.

- `--agent <name>`: adapter name; default is `claude`. See [adapters.md](adapters.md) for available adapters.
- `--headless`: run the agent in the background and write agent output to `agent.log`.
- `--notify`: send a native desktop notification when the headless agent finishes. No-op when `--headless` is not set. See [Desktop notifications](#desktop-notifications).
- `--quiet`: suppress agent log output in the terminal; show only the spinner (TTY) or heartbeat lines (non-TTY/CI). Has no effect with `--headless`.
- `--sdd=<name>`: opt into an SDD methodology (e.g. `--sdd=plain`, `--sdd=speckit`). Omit to skip SDD and work directly toward a PR. See [sdd.md](sdd.md).
- `<issue-number-or-url>`: a bare GitHub issue number (e.g. `42`) **or** a full GitHub issue URL (e.g. `https://github.com/owner/repo/issues/42`). When a URL is supplied, `agentctl` locates or clones the target repository automatically so you do not need to `cd` into it first.
- `[slug]`: optional branch/worktree slug. If omitted, `agentctl` uses `gh issue view` to fetch the issue title and derive a slug.

Side effects:

- Creates a branch named `<issue>-<slug>`.
- Creates a worktree at `../<repo>-<issue>-<slug>/`.
- Reserves a dev-server port in the `3010-3100` range.
- Writes `.agent` metadata in the worktree.
- Seeds `.env.local` from the primary worktree when present, stripping any existing `PORT=` lines (does not inject `PORT=<port>` itself).
- Starts the dev server: if `.agentctl.yml` defines a `dev_server` command it is used (with `{port}` substituted); otherwise a warning is printed and the step is skipped. Any project that needs a dev server must set `dev_server` explicitly in `.agentctl.yml`.
- Launches the selected adapter.
- When the agent exits, appends `Closes #<issue>` to the PR body retrieved via `gh pr view <branch>`, unless the body already contains `closes #<issue>` or `fixes #<issue>` (case-insensitive). Other closing keywords such as `resolves #<issue>` do not suppress the append.

### `agentctl resume`

```bash
agentctl resume [--headless] [--notify] [--quiet] <issue-number>
agentctl resume [--headless] [--notify] [--quiet] <issue-number> [feedback]
```

Resumes a paused agent after the spec-review checkpoint.

- Without feedback: approves the spec and the agent begins implementation.
- With feedback: sends the revision text and the agent rewrites the spec.

By default the resumed agent streams its output to the terminal (foreground mode), identical to `agentctl start` without `--headless`. Press Ctrl+C to detach without stopping the agent.

- `--headless`: run the resumed agent in the background and write output to `agent.log`.
- `--notify`: send a native desktop notification when the headless agent finishes. No-op when `--headless` is not set. See [Desktop notifications](#desktop-notifications).
- `--quiet`: suppress agent log output in the terminal; show only the spinner (TTY) or heartbeat lines (non-TTY/CI). Has no effect with `--headless`.

The command requires:

- A linked worktree for the issue.
- A `.agent` metadata file with the selected agent and session ID.
- A generated spec at `specs/*/spec.md`.

### `agentctl status`

```bash
agentctl status
agentctl status --verbose
agentctl list
```

Shows all linked worktrees and their current state.

Default columns:

```text
ISSUE  BRANCH  AGENT  PORT  SPEC  PR
```

Verbose columns:

```text
ISSUE  BRANCH  AGENT  PATH  PORT  DEV-PID  AGENT-PID  SPEC  PR  SESSION
```

Spec states:

- `no-spec`: no spec found for the issue.
- `paused`: `spec.md` exists, but no `plan.md` or `tasks.md`.
- `in-progress`: `plan.md` exists, but no `tasks.md`.
- `done`: `tasks.md` exists.

PR column shows the PR number and state from `gh pr view <branch>`, e.g. `#42 OPEN`, `#42 MERGED`. Shows `none` when no PR exists for the branch.

### `agentctl cleanup`

```bash
agentctl cleanup [issue-number]
agentctl cleanup --all
```

Cleans up a worktree after its PR is merged.

Behavior:

- Infers the issue number from the current branch when run inside a linked worktree and no issue is provided.
- Ensures the primary worktree is on `main`.
- Verifies the branch PR is `MERGED` via `gh`.
- Pulls `main` with fast-forward only.
- Stops recorded dev/agent PIDs when possible.
- Removes the linked worktree.
- Deletes local and remote branches.

Use `--all` to scan all linked worktrees and run the cleanup flow for every branch whose PR state is `MERGED`. Branches without PRs, unmerged PRs, detached worktrees, or branches without a numeric issue prefix are skipped.

If the PR is not merged, use `agentctl discard` for abandoned work.

### `agentctl discard`

```bash
agentctl discard [issue-number]
agentctl discard --stale
```

Permanently discards a worktree and deletes local/remote branches. This is unrecoverable and prompts for `YES`.

Use this for abandoned or failed work where the PR should not be merged.

Like `cleanup`, the issue number can be inferred from the current branch when run inside a linked worktree.

Use `--stale` to discard all linked worktrees at once that have no running agent and no PR. All matching worktrees are listed before the confirmation prompt. `--stale` and a positional issue number are mutually exclusive.

A worktree is considered stale when both conditions hold:
1. No agent is running (no `.agent` file, empty `agent-pid`, or the recorded PID is not alive).
2. No PR of any state exists for the branch (`gh pr list --state all` returns no results).

When `agentctl cleanup --all` skips worktrees with no PR, it prints a hint if any of them are also stale:

```
Note: N stale worktree(s) found with no agent and no PR — run `agentctl discard --stale` to remove them.
```

### `agentctl logs`

```bash
agentctl logs <issue-number>
agentctl logs <issue-number> --lines 100
agentctl logs <issue-number> --no-follow
```

Streams `agent.log` for the given issue to stdout.

Flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--lines N` | `50` | Lines of history to show before following |
| `--no-follow` | `false` | Print history and exit without following |

Behavior:

- Looks up the worktree path from state (same as `agentctl status`).
- Prints the last `--lines N` lines of `agent.log`.
- Then follows new output until Ctrl+C (unless `--no-follow` is set).
- If `agent.log` does not exist yet, waits up to 10 seconds for it to appear.

Error cases:

| Condition | Error message |
|-----------|---------------|
| Issue not found | `no worktree found for issue N — has it been started?` |
| `agent.log` missing after 10s | `agent log not found — is the agent running? (looked for <path>)` |

### `agentctl attach`

```bash
agentctl attach <issue-number>
```

Streams `agent.log` and exits automatically when the agent process finishes — mirrors the non-headless `start` experience for an already-running headless agent.

Behavior:

- Looks up the worktree path from state and reads the agent PID from `.agent`.
- If the agent is already dead: prints the last 50 lines of `agent.log` and prints `agent has already finished`.
- If the agent is still running: streams `agent.log` to stdout and exits when the process ends.
- On Ctrl+C: prints `agent still running in background (pid N)` and exits without killing the agent.

Error cases:

| Condition | Error message |
|-----------|---------------|
| Issue not found | `no worktree found for issue N — has it been started?` |
| `.agent` file missing or no PID | `no agent PID recorded for issue N — was it started headless?` |
| `agent.log` missing after 10s | `agent log not found — is the agent running? (looked for <path>)` |

## Workflows

### Interactive single-issue workflow

```bash
# From your application repo's primary worktree
agentctl start 42

# Or using a full GitHub issue URL (works from any directory)
agentctl start https://github.com/owner/repo/issues/42
```

The agent runs in your terminal with its log streamed live so you can follow along. Without `--sdd`, the agent works directly toward a PR. Use `--sdd=plain` or `--sdd=speckit` after the issue number to add a spec-review checkpoint.

To suppress log output and show only a spinner/heartbeat:

```bash
agentctl start --quiet 42
```

To find the dev-server port at any point (e.g. to review the running app after the agent opens a PR):

```bash
agentctl status
```

The `PORT` column shows the reserved port for each worktree. The dev server stays running until you clean up.

After the PR is merged:

```bash
agentctl cleanup 42
```

### Headless single-issue workflow

```bash
# Start work in the background; get a desktop notification when done
agentctl start --headless --notify 42

# Watch progress
agentctl status --verbose
agentctl logs 42

# Attach and wait for the agent to finish
agentctl attach 42

# Approve the spec after review (streams to terminal by default)
agentctl resume 42

# Or request revisions instead
agentctl resume 42 "Narrow scope to the API layer; avoid UI changes."

# To resume in background (matches original headless behaviour)
agentctl resume --headless 42

# Clean up after merge
agentctl cleanup 42
```

### Batch headless workflow

```bash
# Start several issues
for i in 210 211 212; do
  agentctl start --headless "$i"
done

# Review generated specs, then approve each one in background (batch-friendly)
for i in 210 211 212; do
  agentctl resume --headless "$i"
done

# Monitor all worktrees
agentctl status
agentctl status --verbose

# Sweep merged PRs
agentctl cleanup --all
```

### Spec-driven development (SDD)

`agentctl start 42` works out of the box for any repo — by default there is no spec step and the agent opens a PR directly.

Use `--sdd=plain` to add a lightweight spec-review checkpoint (no external tooling required):

```bash
agentctl start 42 --sdd=plain
```

Use `--sdd=speckit` to opt into the Spec Kit workflow if your repo is set up for it:

```bash
agentctl start 42 --sdd=speckit
```

See [sdd.md](sdd.md) for the SDD methodology schema and drop-in locations.

### Recovery and maintenance

Discard abandoned work:

```bash
agentctl discard 42
```

Run cleanup/discard from inside a linked worktree without passing the issue number:

```bash
cd ../myrepo-42-my-feature
agentctl cleanup
# or
agentctl discard
```

Inspect logs and state:

```bash
agentctl status --verbose
cat ../<repo>-42-<slug>/.agent
tail -f ../<repo>-42-<slug>/agent.log
tail -f ../<repo>-42-<slug>/dev.log
```

## `.agentctl.yml` configuration

Place `.agentctl.yml` in the root of your application repository to set per-repo defaults. All fields are optional.

```yaml
# Shell command to start the dev server. {port} is substituted with the
# reserved port. If omitted, no dev server is started and a warning is printed.
dev_server: "uvicorn main:app --port {port}"

# Port last allocated by agentctl for the dev server. This field is written
# back by agentctl after it picks a free port in the 3010–3100 range.
# It is managed by agentctl and should not be set manually.
port: 3010

# Send a native desktop notification when a headless agent finishes.
# Can also be enabled per-invocation with --notify.
notify: true
```

## Desktop notifications

agentctl can fire a native OS notification the moment a headless agent finishes, so you don't have to poll `agentctl status` or watch `agent.log`.

### Enabling notifications

**Per-invocation** — pass `--notify` on `start` or `resume`:

```bash
agentctl start --headless --notify 42
agentctl resume --headless --notify 42
```

**Repo-wide default** — add `notify: true` to `.agentctl.yml`:

```yaml
notify: true
```

Either or both can be set; they combine with OR. `--notify` has no effect without `--headless`.

### Notification format

```
Title:   agentctl
Message: Agent finished — issue #42 (42-my-feature): succeeded
```

The message includes the issue number, branch name, and exit status (`succeeded` or `failed`).

### Platform support

| Platform | Tool | Notes |
|----------|------|-------|
| macOS | `osascript` | Built into macOS; no install required |
| Linux | `notify-send` | Provided by `libnotify-bin` on Debian/Ubuntu |
| Other | — | Silently no-ops; CI is unaffected |

## Worktree state files

Each started worktree contains:

```text
.agent          key=value metadata (agent, session-id, dev-pid, agent-pid)
agent.log       coding-agent output (streamed to terminal in interactive mode; only file in headless mode)
dev.log         dev-server output
specs/          SDD spec artifacts (e.g. spec.md, plan.md, tasks.md) when using SDD
```

The primary worktree is the first worktree reported by `git worktree list --porcelain`; linked worktrees are created next to it.

## Related docs

- [install.md](install.md) — prerequisites and installation.
- [sdd.md](sdd.md) — SDD overview, methodology schema, and drop-in locations.
- [development.md](development.md) — adapter contract, build, testing, and CI.
