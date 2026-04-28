# Introducing agentctl

AI coding agents like [Claude Code](https://claude.ai/code), [Codex](https://github.com/openai/codex), and [Copilot](https://github.com/features/copilot) have become genuine productivity multipliers. Point one at a GitHub issue and it writes code, runs tests, and opens a pull request — often with minimal intervention. But what happens when you want to work on ten issues at once, or switch between different coding agents depending on the task?

Beyond parallelism, the industry has seen a wave of Spec-Driven Development (SDD) methodologies — frameworks like [Spec Kit](https://github.com/github/spec-kit), [AgentOS](https://github.com/arun-gupta/agentctl/issues/35), [OpenSpec](https://github.com/arun-gupta/agentctl/issues/38), and [Kiro-style specs](https://github.com/arun-gupta/agentctl/issues/39) that inject a human review checkpoint before an agent begins writing code. agentctl integrates all of these into a unified SDLC workflow, with human-in-the-loop at exactly the right moments.

This article walks through what agentctl is, why it exists, and how to get started.

## The problem

Running a single AI agent in a terminal works fine. Running several simultaneously surfaces a class of problems that agents themselves can't solve:

- **Git state collisions.** Agents commit to branches. Without isolation, two agents in the same working tree will conflict with each other and with your own uncommitted edits.
- **Port conflicts.** Each agent may optionally start a dev server. Without coordination, the second agent fails to bind `localhost:3000` because the first already holds it.
- **No shared visibility.** A fleet of terminal tabs gives you no consolidated view of what's running, what's paused waiting for review, and what's already opened a PR.
- **Manual lifecycle management.** Starting, watching, and cleaning up worktrees by hand is tedious and error-prone at scale.
- **Agent lock-in.** Different tasks suit different coding agents. Switching from Claude to Codex today means re-configuring your workflow from scratch.
- **SDD integration.** Each SDD methodology — Spec Kit, plain, AgentOS, and others — has its own lifecycle, conventions, and commands, requiring you to learn and configure each one separately with no portability across agents or projects. Coordinating spec-driven workflows across parallel agents is error-prone without dedicated tooling.

```mermaid
flowchart TD
    dev["👤 Developer\n(multiple terminal tabs)"] --> wt[Single working tree]
    a1["🤖 Agent on #42"] --> wt
    a2["🤖 Agent on #43"] --> wt
    a3["🤖 Agent on #44"] --> wt
    wt --> pain["❌ Git conflicts\n❌ Port collisions\n❌ No consolidated status\n❌ Manual lifecycle management"]
```

agentctl solves all of the above. Every coding agent and every SDD methodology is pluggable — you can mix and match them per issue.

## What agentctl does

agentctl is a CLI that manages the full lifecycle of AI coding agents working on GitHub issues. For each issue number passed to it:

1. **Isolated worktree** — agentctl creates a [linked Git worktree](https://git-scm.com/docs/git-worktree) at `../<repo>-<issue>-<slug>/`. Every agent works in its own directory with its own branch; they never share an index or conflict with your primary checkout.
2. **Reserved port** *(optional)* — when a dev server is configured, agentctl picks a free port in the `3010–3100` range, persists `port: <port>` to `.agentctl.yml`, and starts the dev server there. No two agents fight over the same port.
3. **Structured lifecycle** — each worktree gets a `.agent` metadata file that tracks the agent name, session ID, and process IDs. `agentctl status` reads these across all worktrees and shows a consolidated table. Once the agent opens a PR, the `PR` column updates to show the PR number and state (e.g. `#42 OPEN`).
4. **One command per issue** — `agentctl start 42` handles worktree creation, environment seeding, dev server startup, and agent launch. When the agent opens a PR, agentctl automatically appends `Closes #42` to the PR body so GitHub links the issue. `agentctl cleanup 42` reverses it after the PR merges.

```mermaid
flowchart LR
    cmd["agentctl start 42"] --> wt["Isolated worktree\n../<repo>-42-<slug>/"]
    cmd --> port["Reserved port\n3010–3100"]
    cmd --> meta[".agent state file"]
    cmd --> agent["Coding agent"]
    wt & port & meta --> status["agentctl status\n(consolidated view)"]
    agent --> pr["Pull request"]
```

agentctl is **agent-agnostic and fully pluggable**. The default adapter is Claude Code (`claude`), and `--agent codex` and `--agent copilot` are tested built-ins. `--agent gemini` and `--agent opencode` are also included and would benefit from additional community testing. Adding support for a new coding agent takes a single line of YAML:

```yaml
binary: cursor-agent
```

Optional fields let you customise prompt flags, session IDs, and full command strings. Drop the YAML file into `.agentctl/adapters/` and the agent is immediately available. See [adapters.md](../adapters.md) for the full schema, built-in adapters, and drop-in locations.

## Quick start

```mermaid
flowchart LR
    A["agentctl start 42"] --> B[Create isolated\nworktree + branch]
    B --> C["Reserve port\n3010–3100 (optional)"]
    C --> D["Start dev server\n(optional)"]
    D --> E[Launch coding agent]
    E --> F[Agent writes code,\nruns tests]
    F --> G[Agent opens PR]
    G --> H["agentctl cleanup 42"]
```

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

agentctl creates a linked worktree, optionally reserves a port and starts the dev server (when a dev server is configured in `.agentctl.yml`), and launches the coding agent — all in one step. Agent output streams live to your terminal so you can follow along. Press Ctrl+C at any time to detach — the agent keeps running in the background and you get your prompt back.

To suppress log output and show only a progress indicator:

```bash
agentctl start --quiet 42
```

When the agent finishes and opens a PR, agentctl prints the PR link:

```text
PR: #42  https://github.com/owner/repo/pull/42
```

### Check what's running

While the agent works, open a second terminal and run:

```bash
agentctl status
```

Output looks like:

```text
ISSUE  BRANCH            AGENT   PORT  SPEC      PR
42     42-fix-login      claude  3010  no-spec   #42 OPEN
```

The `ISSUE` column is your key. `PORT` tells you where the dev server is listening. `SPEC` shows which SDD methodology is active — `no-spec` means the agent is working directly toward a PR with no spec checkpoint; a value like `plain` or `speckit` means spec-driven development is enabled for that issue. `PR` shows the PR number and state once the agent opens a pull request; it updates to `MERGED` after merge.

### Clean up after merge

Once the PR for issue 42 is merged:

```bash
agentctl cleanup 42
```

This pulls `main`, stops the dev server and agent processes, removes the linked worktree, and deletes the local and remote branches. Your primary checkout is left clean.

The full single-issue lifecycle looks like this:

```mermaid
flowchart LR
    A["agentctl start 42"] --> B[Agent working]
    B --> C["agentctl status\n(check any time)"]
    C --> B
    B --> D[Agent opens PR]
    D --> E[PR merged]
    E --> F["agentctl cleanup 42"]
```

## Configuring `.agentctl.yml`

The `.agentctl.yml` file lives at the root of your repo. It is the single source of truth for per-repo agentctl settings. You only need to add it when you want a dev server; everything else works without it.

| Field | Who writes it | Description |
|---|---|---|
| `dev_server` | Developer | Command to start the dev server. Use `{port}` as a placeholder — agentctl substitutes the allocated port. Example: `"uvicorn main:app --port {port}"` |
| `port` | agentctl | The port allocated for the dev server. Written back automatically; treat it as read-only. |

A minimal example that enables a dev server:

```yaml
# .agentctl.yml
dev_server: "npm run dev -- --port {port}"
```

After `agentctl start 42` reserves a port, agentctl writes the value back:

```yaml
# .agentctl.yml (after agentctl start)
dev_server: "npm run dev -- --port {port}"
port: 3010
```

Both fields are optional. Omitting `dev_server` means agentctl skips port reservation and dev server startup entirely.

## Headless / batch mode

The quick-start workflow keeps the agent attached to your terminal. For running several issues in parallel — or for CI-style automation — pass a comma-separated list of issue numbers and agentctl runs all of them headlessly:

```mermaid
flowchart LR
    A["agentctl start 210,211,212"] --> B[3 isolated worktrees\nrunning in parallel]
    B --> C[Agent 210\nwrites code]
    B --> D[Agent 211\nwrites code]
    B --> E[Agent 212\nwrites code]
    C --> F[PR for #210]
    D --> G[PR for #211]
    E --> H[PR for #212]
```

```bash
# Start three issues in parallel (--headless is implicit for batch)
agentctl start 210,211,212
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

Attach and wait until an agent finishes — identical to the interactive experience, but for an already-running headless agent. Ctrl+C detaches without killing the agent:

```bash
agentctl attach 211
```

When all three have opened PRs, sweep everything in one pass:

```bash
agentctl cleanup --all   # cleans up any worktree whose PR is MERGED
```

For abandoned work (no PR, or a PR that should not be merged), use `agentctl discard`:

```bash
agentctl discard 211
```

## Spec-driven development (SDD)

By default, `agentctl start` sends the agent straight to implementation: write code, push branch, open PR. This works well when the GitHub issue already includes enough implementation detail for the agent to proceed confidently.

When the issue is high-level or leaves room for interpretation, it's better to route through SDD: have the agent first write a spec describing its intended approach, review it yourself, then let the agent proceed to code. This human-in-the-loop checkpoint prevents expensive rework when the agent's interpretation diverges from your intent.

```mermaid
flowchart LR
    A["agentctl start 42 --sdd=plain"] --> B["Stage 1: Agent writes\nspecs/spec.md"]
    B --> C{"👤 Human reviews spec\n(checkpoint 1)"}
    C -->|"agentctl resume 42"| D["Stage 2: Agent implements\ncode & runs tests"]
    C -->|"agentctl resume 42 'revise...'"| B
    D --> E[Agent opens PR]
    E --> F{"👤 Human reviews PR\n(checkpoint 2)"}
    F -->|Merge| G[Done ✓]
    F -->|Request changes| D
```

### Pluggable SDD

SDD in agentctl is fully pluggable. [`plain`](../sdd.md#plain) is the default built-in methodology — a lightweight single-file spec workflow with one approval gate and no slash commands. Use it in any repo without any setup:

```bash
agentctl start 42 --sdd=plain
```

The agent's work now has two stages:

1. **Stage 1 — spec.** The agent writes `specs/spec.md` describing its intended approach, then pauses and waits for your review. A good plain spec covers: the problem being solved, the planned approach and changes, and anything explicitly out of scope — see the [spec template](../spec-template.md) for the full prescribed format. **This is human-in-the-loop checkpoint #1.**
2. **Stage 2 — implementation.** After your review and approval, the agent continues to a PR. **The PR review itself is human-in-the-loop checkpoint #2.**

To approve and let the agent proceed:

```bash
agentctl resume 42
```

To send revision feedback instead:

```bash
agentctl resume 42 "Narrow scope to the API layer; avoid UI changes."
```

For a real example of this flow, see [agentctl-test issue #15](https://github.com/arun-gupta/agentctl-test/issues/15).

### Other SDD methodologies

The `--sdd=plain` methodology requires no external tooling — it works in any repo. For repos configured with Spec Kit, `--sdd=speckit` runs a richer four-stage lifecycle. You can also define your own methodology by dropping a YAML file into `.agentctl/sdd/`. See [sdd.md](../sdd.md) for details.

More built-in methodologies are planned — each tracked as an open issue and available today as a custom YAML drop-in:

| Methodology | Tracking issue |
|-------------|---------------|
| AgentOS | [#35](https://github.com/arun-gupta/agentctl/issues/35) |
| Specs.MD | [#36](https://github.com/arun-gupta/agentctl/issues/36) |
| OpenSpec | [#38](https://github.com/arun-gupta/agentctl/issues/38) |
| Kiro-style specs | [#39](https://github.com/arun-gupta/agentctl/issues/39) |

## What's next

- **[install.md](../install.md)** — full prerequisites, Homebrew, prebuilt binaries, and source builds.
- **[cli.md](../cli.md)** — complete command reference: every flag, every workflow, state files, and recovery operations.
- **[sdd.md](../sdd.md)** — SDD overview, built-in methodologies (`plain`, `speckit`), the YAML schema for custom methodologies, and drop-in locations.
- **[adapters.md](../adapters.md)** — YAML adapter schema for adding new coding agents, built-in adapter reference, and drop-in locations.

## This is evolving work — we'd love your help

agentctl is actively evolving. The patterns it encodes — parallel worktrees, reserved ports, spec-driven checkpoints — have emerged from real workflows, but every project is different.

If you try agentctl on your own codebase and run into something that doesn't fit, we want to hear about it. [Open an issue](https://github.com/arun-gupta/agentctl/issues) describing your use case: the kind of repo, the agents you're running, and where agentctl falls short. That feedback directly shapes what gets built next.
