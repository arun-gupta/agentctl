# Introducing agentctl

AI coding agents like Claude Code, Codex, and Copilot have become genuine productivity multipliers. Point one at a GitHub issue and it writes code, runs tests, and opens a pull request — often with minimal intervention. But what happens when you want to work on ten issues at once?

This article walks through what agentctl is, why it exists, and how to get started.

## The problem

Running a single AI agent in a terminal works fine. Running several simultaneously surfaces a class of problems that agents themselves can't solve:

- **Git state collisions.** Agents commit to branches. Without isolation, two agents in the same working tree will conflict with each other and with your own uncommitted edits.
- **Port conflicts.** Each agent typically starts a dev server. Without coordination, the second agent fails to bind `localhost:3000` because the first already holds it.
- **No shared visibility.** A fleet of terminal tabs gives you no consolidated view of what's running, what's paused waiting for review, and what's already opened a PR.
- **Manual lifecycle management.** Starting, watching, and cleaning up worktrees by hand is tedious and error-prone at scale.

agentctl solves all four.

## What agentctl does

agentctl is a CLI that manages the full lifecycle of AI coding agents working on GitHub issues. For each issue you give it:

1. **Isolated worktree** — agentctl creates a [linked Git worktree](https://git-scm.com/docs/git-worktree) at `../<repo>-<issue>-<slug>/`. Every agent works in its own directory with its own branch; they never share an index or conflict with your primary checkout.
2. **Reserved port** — agentctl picks a free port in the `3010–3100` range, writes `PORT=<port>` into the worktree's `.env.local`, and starts the dev server there. No two agents fight over the same port.
3. **Structured lifecycle** — each worktree gets a `.agent` metadata file that tracks the agent name, session ID, and process IDs. `agentctl status` reads these across all worktrees and shows a consolidated table.
4. **One command per issue** — `agentctl start 42` handles worktree creation, environment seeding, dev server startup, and agent launch. `agentctl cleanup 42` reverses it after the PR merges.

agentctl is agent-agnostic. The default adapter is Claude Code (`claude`), but `--agent codex`, `--agent copilot`, `--agent gemini`, and `--agent opencode` are all available. See [install.md](../install.md) for prerequisites.

## Quick start

### Install

```bash
# macOS / Linux via Homebrew (recommended)
brew install arun-gupta/tap/agentctl
```

See [install.md](../install.md) for prebuilt binaries and source builds.

### Start an agent on an issue

```bash
# From your application repo's primary worktree
agentctl start 42
```

agentctl creates a linked worktree, reserves a port, starts the dev server, and launches Claude Code — all in one step. Agent output streams live to your terminal so you can follow along.

### Check what's running

While the agent works, open a second terminal and run:

```bash
agentctl status
```

Output looks like:

```text
ISSUE  BRANCH            AGENT   PORT  SPEC      PR
42     42-fix-login      claude  3010  no-spec   none
```

The `ISSUE` column is your key. `PORT` tells you where the dev server is listening. `PR` updates once the agent opens a pull request.

### Clean up after merge

Once the PR for issue 42 is merged:

```bash
agentctl cleanup 42
```

This pulls `main`, stops the dev server and agent processes, removes the linked worktree, and deletes the local and remote branches. Your primary checkout is left clean.

## Headless / batch mode

The quick-start workflow keeps the agent attached to your terminal. For running several issues in parallel — or for CI-style automation — use `--headless`:

```bash
# Start three issues in parallel
for i in 210 211 212; do
  agentctl start --headless "$i"
done
```

Each agent runs in the background and writes its output to `agent.log` inside its worktree. You get your prompt back immediately.

Monitor the fleet:

```bash
agentctl status
```

Tail a specific agent's log:

```bash
agentctl logs 211
```

Attach and wait until an agent finishes — identical to the interactive experience, but for an already-running headless agent:

```bash
agentctl attach 211
```

When all three have opened PRs, sweep everything in one pass:

```bash
agentctl cleanup --all   # cleans up any worktree whose PR is MERGED
```

## Spec-driven development (SDD)

By default, `agentctl start` sends the agent straight to implementation: write code, push branch, open PR.

If you want a review checkpoint before implementation begins, opt in to spec-driven development with `--sdd=plain`:

```bash
agentctl start 42 --sdd=plain
```

The agent's work now has two stages:

1. **Stage 1 — spec.** The agent writes `specs/spec.md` describing its intended approach, then pauses.
2. **Stage 2 — implementation.** After your review, you approve (or revise) and the agent continues to a PR.

To approve and let the agent proceed:

```bash
agentctl resume 42
```

To send revision feedback instead:

```bash
agentctl resume 42 "Narrow scope to the API layer; avoid UI changes."
```

The `--sdd=plain` methodology requires no external tooling — it works in any repo. For repos configured with Spec Kit, `--sdd=speckit` runs a richer four-stage lifecycle. You can also define your own methodology by dropping a YAML file into `.agentctl/sdd/`. See [sdd.md](../sdd.md) for details.

## What's next

- **[install.md](../install.md)** — full prerequisites, Homebrew, prebuilt binaries, and source builds.
- **[cli.md](../cli.md)** — complete command reference: every flag, every workflow, state files, and recovery operations.
- **[sdd.md](../sdd.md)** — SDD overview, built-in methodologies (`plain`, `speckit`), the YAML schema for custom methodologies, and drop-in locations.
