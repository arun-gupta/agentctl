# CLI reference and workflows

Canonical command reference and operating workflows for **agentctl**.

Run `agentctl --help` or `agentctl <command> --help` for generated help from the binary.

## Command reference

### `agentctl start`

```bash
agentctl start [--agent <name>] [--headless] [--no-sdd] <issue-number> [slug]
```

Creates a linked worktree for a GitHub issue and launches the selected coding agent inside it.

- `--agent <name>`: adapter name; default is `claude`. See [adapters.md](adapters.md) for available adapters.
- `--headless`: run the agent in the background and write agent output to `agent.log`.
- `--no-sdd`: skip the SDD lifecycle entirely and work directly toward a PR (no spec-review pause).
- `--sdd <name>`: select an SDD methodology (default: `plain`; e.g. `speckit`, `plain`, or a custom methodology). See [sdd.md](sdd.md).
- `<issue-number>`: GitHub issue number.
- `[slug]`: optional branch/worktree slug. If omitted, `agentctl` uses `gh issue view` to fetch the issue title and derive a slug.

Side effects:

- Creates a branch named `<issue>-<slug>`.
- Creates a worktree at `../<repo>-<issue>-<slug>/`.
- Reserves a dev-server port in the `3010-3100` range.
- Writes `.agent` metadata in the worktree.
- Seeds `.env.local` from the primary worktree when present and appends `PORT=<port>`.
- Runs `npm install --silent` and starts `npm run dev -- -p <port>`.
- Launches the selected adapter.

### `agentctl approve-spec`

```bash
agentctl approve-spec <issue-number>
```

Resumes a paused headless run after you approve the generated spec. This sends the approval prompt (`proceed`) to the adapter's `agent_resume` function.

The command requires:

- A linked worktree for the issue.
- A `.agent` metadata file with the selected agent and session ID.
- A generated spec at `specs/*/spec.md`.

### `agentctl revise-spec`

```bash
agentctl revise-spec <issue-number> <feedback>
```

Resumes a paused headless run with revision feedback instead of approval. Feedback must be non-empty.

Use this when the generated spec needs changes before implementation begins.

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

PR states come from `gh pr view <branch>` and are usually `none`, `OPEN`, `MERGED`, or `CLOSED`.

### `agentctl cleanup-merged`

```bash
agentctl cleanup-merged [issue-number]
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

If the PR is not merged, use `agentctl discard` for abandoned work.

### `agentctl cleanup-all-merged`

```bash
agentctl cleanup-all-merged
```

Scans linked worktrees and runs the merged cleanup flow for each branch whose PR state is `MERGED`.

Branches without PRs, unmerged PRs, detached worktrees, or branches without a numeric issue prefix are skipped.

### `agentctl discard`

```bash
agentctl discard [issue-number]
```

Permanently discards a worktree and deletes local/remote branches. This is unrecoverable and prompts for `YES`.

Use this for abandoned or failed work where the PR should not be merged.

Like `cleanup-merged`, the issue number can be inferred from the current branch when run inside a linked worktree.

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
```

The agent runs interactively in your terminal. With the default plain spec workflow, review the generated spec when the agent stops, then tell the agent to continue in the interactive session.

After the PR is merged:

```bash
agentctl cleanup-merged 42
```

### Headless single-issue workflow

```bash
# Start work in the background
agentctl start --headless 42

# Watch progress
agentctl status --verbose
agentctl logs 42

# Attach and wait for the agent to finish
agentctl attach 42

# Approve the spec after review
agentctl approve-spec 42

# Or request revisions instead
agentctl revise-spec 42 "Narrow scope to the API layer; avoid UI changes."

# Clean up after merge
agentctl cleanup-merged 42
```

### Batch headless workflow

```bash
# Start several issues
for i in 210 211 212; do
  agentctl start --headless "$i"
done

# Review generated specs, then approve each one
for i in 210 211 212; do
  agentctl approve-spec "$i"
done

# Monitor all worktrees
agentctl status
agentctl status --verbose

# Sweep merged PRs
agentctl cleanup-all-merged
```

### Repo without Spec Kit

`agentctl start 42` works out of the box for any repo — the default `plain` methodology requires no external tooling.

Use `--sdd speckit` to opt into the Spec Kit workflow if your repo is set up for it:

```bash
agentctl start --sdd speckit 42
```

Use `--no-sdd` for fully automated runs with no spec-review pause:

```bash
agentctl start --no-sdd 42
```

This skips the spec-review pause. The agent works directly toward a PR.

See [sdd.md](sdd.md) for default SDD behavior and target-repo expectations.

### Recovery and maintenance

Discard abandoned work:

```bash
agentctl discard 42
```

Run cleanup/discard from inside a linked worktree without passing the issue number:

```bash
cd ../myrepo-42-my-feature
agentctl cleanup-merged
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

## Worktree state files

Each started worktree contains:

```text
.agent          key=value metadata (agent, session-id, dev-pid, agent-pid)
agent.log       coding-agent output in headless mode
dev.log         dev-server output
specs/          SDD spec artifacts (e.g. spec.md, plan.md, tasks.md) when using SDD
```

The primary worktree is the first worktree reported by `git worktree list --porcelain`; linked worktrees are created next to it.

## Related docs

- [install.md](install.md) — prerequisites and installation.
- [sdd.md](sdd.md) — SDD overview, methodology schema, and drop-in locations.
- [development.md](development.md) — adapter contract, build, testing, and CI.
