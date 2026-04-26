# SDD methodologies

## Overview

By default, **agentctl** has no spec step — the agent works directly toward a PR with no checkpoint.

Use `--sdd <name>` to opt into a spec-driven development (SDD) methodology. The selected methodology defines a kickoff prompt that instructs the agent to follow a spec lifecycle with a human-in-the-loop pause:

1. **Stage 1** — The agent writes a spec, then stops for your approval or revision. In headless mode use `agentctl approve-spec` and `agentctl revise-spec`.
2. **Stage 2** — After approval, the agent implements the changes, pushes the branch, and opens a PR.

```bash
agentctl start --sdd plain 42
```

## How it works

One code path handles all methodologies. Built-in and user-defined methodologies are the same type, loaded by the same loader. The binary ships with built-in methodologies (`speckit`, `plain`) embedded directly as plain YAML files, not special Go code.

Select a methodology with `agentctl start --sdd <name>`. Omit `--sdd` to skip SDD entirely.

## Methodology resolution

Resolved in priority order — first match wins:

| Priority | Location | Path |
|----------|----------|------|
| 1 (highest) | **Project-local** | `.agentctl/sdd/<name>.yml` (relative to cwd) |
| 2 | **User-level** | `~/.config/agentctl/sdd/<name>.yml` |
| 3 (lowest) | **Built-in** | Embedded in the binary |

Both `.yml` and `.yaml` are accepted. When both exist in the same directory, `.yml` wins and agentctl prints a warning to stderr. Dropping a file at level 1 or 2 with the same name as a built-in overrides it completely — useful for customising the kickoff prompt without waiting for a release.

## YAML schema

```yaml
# Full kickoff prompt used when SDD is active.
# Placeholders: {issue}, {port} — substituted via strings.ReplaceAll.
kickoff: |                  # REQUIRED
  Work on GitHub issue #{issue}. Read CLAUDE.md for project conventions.
  Follow the SpecKit lifecycle: run /speckit.specify, then await approval,
  then /speckit.plan, /speckit.tasks, /speckit.implement.
  Dev server is running on port {port}.
```

### Fields

| Field | Required | Description |
|-------|----------|-------------|
| `kickoff` | ✅ | Full prompt text sent to the agent at start time |

### Placeholders

Substituted via `strings.ReplaceAll` (not token-based — kickoff is free-form text):

| Placeholder | Value |
|-------------|-------|
| `{issue}` | GitHub issue number |
| `{port}` | Reserved dev-server port |

Unknown fields are ignored for forward compatibility.

## `--sdd` flag

- `--sdd <name>` opts into the named SDD methodology (e.g. `plain`, `speckit`, or a custom methodology)
- Omitting `--sdd` skips SDD entirely — the agent works directly toward a PR with no spec-review pause

**Generic skip prompt** (hardcoded in Go, used when `--sdd` is omitted):

```
Work on GitHub issue #{issue}. Read CLAUDE.md for project conventions.
Skip the SDD lifecycle — make the changes directly, push the branch,
and open a PR. Do not merge. Dev server is running on port {port}.
```

## Validation

| Condition | Behaviour |
|-----------|-----------|
| `kickoff` missing | Error at load time with file path |
| Invalid YAML | Error at load time with file path |
| Unknown fields | Ignored (forward compatibility) |
| Both `.yml` and `.yaml` exist | `.yml` wins, warning to stderr |
| `--sdd <name>` not found | Error: `unknown SDD methodology "name" — drop name.yml in .agentctl/sdd/ or ~/.config/agentctl/sdd/` |

## Built-in methodologies

### `speckit`

```yaml
kickoff: |
  Work on GitHub issue #{issue}. Read CLAUDE.md for project conventions.
  Follow the SpecKit spec-driven development lifecycle:
  - STAGE 1: Run /speckit.specify to create a spec. Await human approval before continuing.
  - STAGE 2: Run /speckit.plan to break the spec into a plan.
  - STAGE 3: Run /speckit.tasks to create task list.
  - STAGE 4: Run /speckit.implement to implement the tasks.
  Push the branch and open a PR when done. Do not merge.
  Dev server is running on port {port}.
```

### `plain`

A lightweight single-file spec workflow with one approval gate and no slash commands. Use this when the target repository is not set up for Spec Kit.

```yaml
kickoff: |
  Work on GitHub issue #{issue}. Read CLAUDE.md for project conventions.
  Follow the plain spec workflow:
  - STEP 1: Write a `specs/spec.md` describing your intended approach. Stop and wait for human approval.
  - STEP 2: After approval, implement the changes directly, push the branch, and open a PR. Do not merge.
  Dev server is running on port {port}.
```

```bash
agentctl start --sdd plain 42
```

> **Planned:** [#68](https://github.com/arun-gupta/agentctl/issues/68) will add a prescribed `spec.md` format (Problem / Approach / Changes / Out of scope) as a hint in the kickoff prompt so specs have a consistent shape to review.

### Planned built-in methodologies

The following methodologies are tracked as issues and will be added as built-ins once their kickoff prompts are defined:

| Methodology | Issue |
|-------------|-------|
| [AgentOS](https://github.com/arun-gupta/agentctl/issues/35) | [#35](https://github.com/arun-gupta/agentctl/issues/35) |
| [Specs.MD](https://github.com/arun-gupta/agentctl/issues/36) | [#36](https://github.com/arun-gupta/agentctl/issues/36) |
| [OpenSpec](https://github.com/arun-gupta/agentctl/issues/38) | [#38](https://github.com/arun-gupta/agentctl/issues/38) |
| [Kiro-style specs](https://github.com/arun-gupta/agentctl/issues/39) | [#39](https://github.com/arun-gupta/agentctl/issues/39) |

In the meantime, any of these can be used today by dropping a custom YAML file in a drop-in location (see below).

## Drop-in locations

To add or override a methodology without modifying the binary:

**Project-local** (only applies in this repo's working tree):

```
.agentctl/
  sdd/
    mymethod.yml
```

**User-level** (applies to all repos for this user):

```
~/.config/agentctl/
  sdd/
    mymethod.yml
```

Then select it with:

```bash
agentctl start --sdd mymethod 42
```

## Override behaviour

A user-defined methodology with the same name as a built-in completely overrides it. For example, placing a custom `speckit.yml` at `~/.config/agentctl/sdd/speckit.yml` replaces the built-in Spec Kit prompt for all projects.

## Worked example

Add an OpenSpec methodology at the project level:

```bash
mkdir -p .agentctl/sdd
cat > .agentctl/sdd/openspec.yml << 'EOF'
kickoff: |
  Work on GitHub issue #{issue}. Read CLAUDE.md for project conventions.
  Follow the OpenSpec lifecycle — create a spec.md, await approval,
  then implement. Push and open a PR when done. Do not merge.
  Dev server is running on port {port}.
EOF
agentctl start --sdd openspec 42
```

## Related

- [adapters.md](adapters.md) — YAML adapter schema for coding agents (reference implementation)
- [cli.md](cli.md) — command reference and workflows
