# Spec-driven development and Spec Kit

## Default workflow (SDD)

By default, **agentctl** assumes **spec-driven development** with a **human checkpoint**:

1. **Stage 1** — The agent writes a spec, then stops for your approval or revision. In headless mode use `agentctl approve-spec` and `agentctl revise-spec`.
2. **Stage 2** — After approval, the agent runs planning and implementation tasks, then pushes and opens a PR.

That flow is implemented in terms of [**Spec Kit**](https://github.com/github/spec-kit): the kickoff instructs the agent to run `/speckit.specify`, `/speckit.plan`, `/speckit.tasks`, and `/speckit.implement`. **Pause state** is inferred from files under `specs/` in the worktree (for example `spec.md` vs `plan.md` / `tasks.md`).

## What you must provide

**agentctl does not install or vendor Spec Kit.** The **target application repository** (and your coding-agent setup, e.g. Claude Code slash commands) must already support that Spec Kit–style lifecycle.

If the repo is not set up for it, use **`agentctl start --no-speckit`** so the agent skips the spec lifecycle and works straight toward a PR with no spec-review pause.

## Related

- [install.md](install.md) — prerequisites and installing `agentctl`
- [cli.md](cli.md) — CLI usage and workflows
- [development.md](development.md) — adapters, testing, and CI
