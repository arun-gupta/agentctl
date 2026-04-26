# Spec-driven development (SDD)

## Overview

By default, **agentctl** assumes **spec-driven development** with a **human checkpoint**:

1. **Stage 1** — The agent writes a spec, then stops for your approval or revision. In headless mode use `agentctl approve-spec` and `agentctl revise-spec`.
2. **Stage 2** — After approval, the agent runs planning and implementation tasks, then pushes and opens a PR.

The exact lifecycle — what slash commands to run, what files to create — is defined by the **SDD methodology** selected with `--sdd`. The default methodology is **Spec Kit**.

## Selecting a methodology

```bash
agentctl start --sdd speckit 42    # default: Spec Kit
agentctl start --sdd openspec 42   # custom methodology (drop openspec.yml in .agentctl/sdd/)
```

See **[docs/sdd.md](sdd.md)** for the full YAML schema, resolution chain, drop-in locations, and override behaviour.

## Skipping SDD entirely

Use `--no-sdd` when the repository is not set up for spec-driven development, or when you want the agent to make changes and open a PR directly with no spec-review pause:

```bash
agentctl start --no-sdd 42
```

This always uses the hardcoded generic skip prompt, regardless of which methodology is active. The agent is fully automated — no human-in-the-loop checkpoint.

## Default methodology: Spec Kit

The built-in `speckit` methodology instructs the agent to follow the [**Spec Kit**](https://github.com/github/spec-kit) lifecycle: run `/speckit.specify`, then pause for approval, then `/speckit.plan`, `/speckit.tasks`, `/speckit.implement`.

**agentctl does not install or vendor Spec Kit.** The **target application repository** (and your coding-agent setup, e.g. Claude Code slash commands) must already support the Spec Kit lifecycle.

## Related

- [sdd.md](sdd.md) — methodology YAML schema, resolution chain, drop-in locations, override behaviour
- [install.md](install.md) — prerequisites and installing `agentctl`
- [cli.md](cli.md) — CLI usage and workflows
- [development.md](development.md) — adapters, testing, and CI

