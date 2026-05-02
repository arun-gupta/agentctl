# CLI reference and workflows

Canonical command reference and operating workflows for **agentctl**.

Run `agentctl --help` or `agentctl <command> --help` for generated help from the binary.

---

## Table of contents

- [Workflows](#workflows)
  - [Interactive workflow](#interactive-workflow)
  - [Headless workflow](#headless-workflow)
  - [Batch headless workflow](#batch-headless-workflow)
  - [Spec-driven development (SDD)](#spec-driven-development-sdd)
  - [Recovery and maintenance](#recovery-and-maintenance)
- [Command reference](#command-reference)
  - [agentctl start](#agentctl-start)
  - [agentctl logs](#agentctl-logs)
  - [agentctl attach](#agentctl-attach)
  - [agentctl diff](#agentctl-diff)
  - [agentctl resume](#agentctl-resume)
  - [agentctl status](#agentctl-status)
  - [agentctl cleanup](#agentctl-cleanup)
  - [agentctl discard](#agentctl-discard)
  - [agentctl dev start](#agentctl-dev-start)
- [`.agentctl.yml` configuration](#agentctlyml-configuration)
- [Desktop notifications](#desktop-notifications)
- [Worktree state files](#worktree-state-files)
- [Related docs](#related-docs)

---

## Workflows

### Interactive workflow

The agent runs in your terminal with its output streamed live.

```mermaid
flowchart TD
    A[agentctl start 42] --> B[Agent runs in terminal]
    B --> C[PR opened by agent]
    C --> D{Review PR}
    D -- changes needed --> E[agentctl resume 42 feedback]
    E --> B
    D -- approved --> F[Merge PR on GitHub]
    F --> G[agentctl cleanup 42]
```

```bash
# Start — agent streams output to your terminal
agentctl start 42

# Or using a full GitHub issue URL (works from any directory)
agentctl start https://github.com/owner/repo/issues/42

# Suppress log output but still wait for the agent to finish
agentctl start --quiet 42

# Check what the agent has changed so far
agentctl diff 42

# Request changes after reviewing the PR
agentctl resume 42 "Narrow scope to the API layer; avoid UI changes."

# After the PR is merged
agentctl cleanup 42
```

### Headless workflow

The agent runs in the background; you check in at your own pace.

```mermaid
flowchart TD
    A[agentctl start --headless 42] --> B[Agent runs in background]
    B --> C[agentctl logs 42 / agentctl attach 42]
    C --> D[agentctl diff 42]
    D --> E[PR opened by agent]
    E --> F{Review PR}
    F -- changes needed --> G[agentctl resume --headless 42 feedback]
    G --> B
    F -- approved --> H[Merge PR on GitHub]
    H --> I[agentctl cleanup 42]
```

```bash
# Start in the background; get a desktop notification when done
agentctl start --headless --notify 42

# Stream logs live
agentctl logs 42

# Or attach and wait for the agent to finish automatically
agentctl attach 42

# Inspect what the agent changed
agentctl diff 42
agentctl diff 42 --stat

# Request changes (resumes in background to match original headless behaviour)
agentctl resume --headless 42 "Add error handling for the edge case."

# After the PR is merged
agentctl cleanup 42
```

### Batch headless workflow

```bash
# Start several issues at once
agentctl start --headless 210,211,212

# Monitor all worktrees
agentctl status
agentctl status --verbose

# Review what each agent changed
agentctl diff 210 --stat
agentctl diff 211 --stat

# Approve each spec in background
agentctl resume --headless 210
agentctl resume --headless 211
agentctl resume --headless 212

# Sweep all merged PRs
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

```bash
# Discard abandoned work (unrecoverable — prompts for YES)
agentctl discard 42

# Discard all worktrees with no running agent and no PR
agentctl discard --stale

# Run cleanup/discard from inside a linked worktree (no issue number needed)
cd ../myrepo-42-my-feature
agentctl cleanup
agentctl discard

# Inspect state
agentctl status --verbose
cat ../<repo>-42-<slug>/.agent
tail -f ../<repo>-42-<slug>/agent.log
tail -f ../<repo>-42-<slug>/dev.log
```

---

## Command reference

### `agentctl start`

```bash
agentctl start [--agent <name>] [--headless] [--notify] [--quiet] <issue-number-or-url> [slug] [--sdd=<name>]
agentctl start [--agent <name>] [--headless] [--notify] [--quiet] [--sdd=<name>] --task "<description>" [--branch <branch-name>]
```

Creates a linked worktree for a GitHub issue or a free-form task and launches the selected coding agent inside it.

- `--agent <name>`: adapter name; default is `claude`. See [adapters.md](adapters.md) for available adapters.
- `--headless`: run the agent in the background and write agent output to `agent.log`.
- `--notify`: send a native desktop notification when the headless agent finishes. No-op when `--headless` is not set. See [Desktop notifications](#desktop-notifications).
- `--quiet`: suppress all agent log output in the terminal; the parent still waits for the agent to finish (foreground). Has no effect with `--headless`.
- `--sdd=<name>`: opt into an SDD methodology (e.g. `--sdd=plain`, `--sdd=speckit`). Omit to skip SDD and work directly toward a PR. See [sdd.md](sdd.md).
- `--task "<description>"`: start from a free-form task description instead of a GitHub issue.
- `--branch <branch-name>`: explicit branch name for `--task`. Only valid with `--task`.
- `<issue-number-or-url>`: a bare GitHub issue number (e.g. `42`) **or** a full GitHub issue URL (e.g. `https://github.com/owner/repo/issues/42`). When a URL is supplied, `agentctl` locates or clones the target repository automatically so you do not need to `cd` into it first.
- `[slug]`: optional branch/worktree slug. If omitted, `agentctl` uses `gh issue view` to fetch the issue title and derive a slug.

Side effects:

- Issue mode: creates a branch named `<issue>-<slug>` and a worktree at `../<repo>-<issue>-<slug>/`.
- Task mode: creates a branch named `task/<slug>` by default and a worktree at `../<repo>-task-<slug>/`. `--branch` overrides the branch name verbatim and uses the slash-replaced branch name in the worktree path.
- Reserves a dev-server port in the `3010-3100` range.
- Writes `.agent` metadata in the worktree.
- Seeds `.env.local` from the primary worktree when present, stripping any existing `PORT=` lines (does not inject `PORT=<port>` itself).
- Starts the dev server: if `.agentctl.yml` defines a `dev_server` command it is used (with `{port}` substituted); otherwise the step is silently skipped. Any project that needs a dev server must set `dev_server` explicitly in `.agentctl.yml`.
- Launches the selected adapter.
- In issue mode, when the agent exits, appends `Closes #<issue>` to the PR body retrieved via `gh pr view <branch>`, unless the body already contains `closes #<issue>` or `fixes #<issue>` (case-insensitive).

### `agentctl logs`

```bash
agentctl logs <issue-number-or-url>
agentctl logs <issue-number-or-url> --lines 100
agentctl logs <issue-number-or-url> --no-follow
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
agentctl attach <issue-number-or-url>
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

### `agentctl diff`

```bash
agentctl diff <issue-number-or-url>
agentctl diff <issue-number-or-url> --stat
agentctl diff <issue-number-or-url> --base <branch>
agentctl diff <issue-number-or-url> --no-pager
```

Shows the git diff for the agent's worktree without leaving your primary working directory.

Flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--stat` | `false` | Show changed-file summary instead of full diff |
| `--base <branch>` | — | Diff base branch; uses `<branch>...HEAD` (three-dot) to show only commits the agent added |
| `--no-pager` | `false` | Print raw diff to stdout without paging |

Behavior:

- Resolves the worktree path for the given issue (same lookup as `agentctl logs`).
- Without `--base`: runs `git diff HEAD` — shows all uncommitted changes (staged + unstaged).
- With `--base <branch>`: runs `git diff <branch>...HEAD` — shows everything the agent added since branching from `<branch>`.
- Output is piped through `$PAGER` (defaulting to `less -R`) with `--color=always` when stdout is a TTY.
- `--stat` and `--no-pager` skip the pager and print directly to stdout.

Error cases:

| Condition | Error message |
|-----------|---------------|
| Issue not found | `no worktree found for issue N — has it been started?` |

### `agentctl resume`

```bash
agentctl resume [--headless] [--notify] [--quiet] <issue-number-or-url>
agentctl resume [--headless] [--notify] [--quiet] <issue-number-or-url> [feedback]
```

Resumes a paused agent after the spec-review checkpoint.

- Without feedback: approves the spec and the agent begins implementation.
- With feedback: sends the revision text and the agent rewrites the spec.

By default the resumed agent streams its output to the terminal (foreground mode), identical to `agentctl start` without `--headless`. Press Ctrl+C to detach without stopping the agent.

- `--headless`: run the resumed agent in the background and write output to `agent.log`.
- `--notify`: send a native desktop notification when the headless agent finishes. No-op when `--headless` is not set. See [Desktop notifications](#desktop-notifications).
- `--quiet`: suppress all agent log output in the terminal; the parent still waits for the agent to finish (foreground). Has no effect with `--headless`.

`--agent` is not accepted by `resume` — the agent is recorded in `.agent` at `start` time and reused automatically.

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
agentctl cleanup [issue-number-or-url]
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

Use `--all` to scan all linked worktrees and run the cleanup flow for every branch whose PR state is `MERGED`. Also prunes orphaned remote branches (branches with no local worktree whose PR is MERGED or CLOSED). Branches without PRs, unmerged PRs, detached worktrees, or branches without a numeric issue prefix are skipped.

If the PR is not merged, use `agentctl discard` for abandoned work.

### `agentctl discard`

```bash
agentctl discard [issue-number-or-url]
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

### `agentctl dev start`

```bash
agentctl dev start [issue]
agentctl dev start [issue] --quiet
```

Launches the `dev_server` command from `.agentctl.yml` using the port already recorded in `.agent`. Does **not** allocate a new port — use this when the dev server was skipped during `agentctl start` (e.g. `.agentctl.yml` was added mid-PR) or after it crashed.

Run without arguments inside a linked worktree to infer the issue number from the current branch.

Flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--quiet` | `false` | Suppress log streaming; only print the ready URL |

Behavior:

- Reads the port from `.agent` (set by `agentctl start`).
- Checks that the dev server is not already running; exits 1 if it is.
- Starts `dev_server` with `{port}` substituted, writing output to `dev.log`.
- Prints `Dev server ready → http://localhost:<port>` to stdout.
- Sends an OS notification when ready if `notify: true` in `.agentctl.yml`.
- Default: streams `dev.log` to stdout until Ctrl+C or the process exits.
- `--quiet`: prints the URL and exits immediately.
- Records the new dev server PID in `.agent` so `agentctl cleanup` can tear it down.

Error cases:

| Condition | Error message |
|-----------|---------------|
| `dev_server` not set in `.agentctl.yml` | `dev_server not set in .agentctl.yml — nothing to start` |
| `.agent` has no port recorded | `no port recorded in .agent — run agentctl start first` |
| Dev server already running | `dev server already running (PID N) — stop that process and clear the recorded dev-pid before restarting` |
| Port already in use | `port N already in use` (with PID if detectable) |

---

## `.agentctl.yml` configuration

Place `.agentctl.yml` in the root of your application repository to set per-repo defaults. All fields are optional.

```yaml
# Shell command to start the dev server. {port} is substituted with the
# reserved port. If omitted, no dev server is started (silently skipped).
dev_server: "uvicorn main:app --port {port}"

# Port agentctl allocated for the dev server (3010–3100 range). This value is
# written and managed by agentctl; do not set it manually.
port: 3010

# Send a native desktop notification when a headless agent finishes.
# Can also be enabled per-invocation with --notify.
notify: true
```

---

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

---

## Worktree state files

Each started worktree contains:

```text
.agent          key=value metadata (agent, session-id, dev-pid, agent-pid)
agent.log       coding-agent output (streamed to terminal in interactive mode; only file in headless mode)
dev.log         dev-server output
specs/          SDD spec artifacts (e.g. spec.md, plan.md, tasks.md) when using SDD
```

The primary worktree is the first worktree reported by `git worktree list --porcelain`; linked worktrees are created next to it.

---

## Related docs

- [install.md](install.md) — prerequisites and installation.
- [sdd.md](sdd.md) — SDD overview, methodology schema, and drop-in locations.
- [development.md](development.md) — adapter contract, build, testing, and CI.
