# Build Your PLATFORM: 8 Patterns for AI-Assisted Development That Survive Vendor Shifts

Everyone is experimenting with AI coding agents. Far fewer are shipping production software with them reliably.

The models are remarkable, and the gap is in the workflow around them. When teams first adopt AI agents, the instinct is to accelerate the inner loop, which is to write code faster and close issues quicker by dropping an agent into an existing process. It rarely works as expected. The DORA research highlights a familiar discrepancy: high-performing engineering teams don't just move faster, they have fundamentally different processes. The same principle applies to AI agents, context drifts, and agents hallucinate dependencies. One agent undoes what another just wrote. Agents that deliver reliably do so because the workflow around them is designed for them, not retrofitted around them.

Over the past several weeks, I built two open-source projects using AI agents as first-class contributors: [agentctl](https://github.com/arun-gupta/agentctl), a Go CLI that orchestrates coding agents across isolated git worktrees, and [repo-pulse](https://github.com/arun-gupta/repo-pulse), a repository health analytics platform built with Next.js. AI agents helped me accelerate the delivery of both projects significantly, but I also learned many hard lessons along the way about context drift, hallucinated dependencies, conflicting agent sessions, and the limits of automated review. That is what I am sharing here.

I call it PLATFORM. Eight patterns, one per letter, that together turn AI agents from expensive autocomplete into a genuine force multiplier. The framework is deliberately agnostic: it works with any model, any git provider, and any specification methodology. Vendor lock-in is exactly what you do not want in a fast-moving tooling landscape. When your workflow is tightly coupled to a single provider's API or model family, every pricing change and capability shift forces a rebuild. The patterns here survive because they describe how to structure work, not which tool to use. The vendor landscape of 2026 will not look like 2027.

---

## P: Per-Feature Sandbox

The first mistake most teams make is sharing branches between agents and humans, or between multiple agents running in parallel. Shared state means shared risk. One agent rebasing at the wrong moment corrupts another's work. A human pushing a fix lands on top of an in-progress agent session.

The solution is simple: one git worktree per feature, per agent, per task. Not branches on the same checkout, but fully isolated worktrees that can be discarded without affecting anything else. When an agent session goes wrong, you throw the worktree away. No cleanup, no rebase, no untangling.

In agentctl, this is the core primitive. Every `agentctl start` command creates a linked worktree at `../<repo>-<issue>-<slug>/`, provisions the environment, and writes a `.agent` metadata file. Beyond simply starting an agent, agentctl supports a headless mode: provide a GitHub issue number and the agent runs autonomously, opens a PR when complete, and notifies you — no supervision required during the session. You can also optionally have the agent generate a structured spec before implementation begins, useful when the issue description alone is underspecified. For projects that need a running process, there is an option to start a dev server in the language of the worktree so the agent can exercise the code as it builds. When the work is done, `agentctl cleanup` removes it — or `agentctl clean` for a faster sweep. The main worktree never sees the mess.

For repo-pulse, I ran multiple feature agents in parallel, each in its own worktree, each working on a separate GitHub issue. None of them could interfere with each other. When two agents happened to touch the same utility function, the conflict surfaced at PR review time, not mid-session, and was resolved once.

The pattern costs you nothing except disk space.

---

## L: Law of the Land

Agents are eager to help and have no persistent memory across sessions. Without a written source of truth, every session starts from scratch: the agent infers your stack from the files it can see, guesses your testing philosophy from the existing tests, and makes decisions about accuracy, security, and workflow that you never explicitly endorsed.

There is also a deeper problem that surfaces when multiple agents work concurrently: agents have no cross-agent memory. Each agent has memory within its own session, but there is no shared state between a Claude session running in one worktree and a Codex session running in another. When both need to make architectural decisions, they will make them independently. Tools like `AGENTS.md` (used by Copilot) and `CLAUDE.md` (used by Claude Code) serve as project constitutions that every agent picks up automatically — but they are static. They describe what the project is; they do not capture what each agent has already done, what decisions were made in a previous session, or which areas of the codebase are currently being modified by a parallel agent. That gap is real, and it means the constitution document needs to be written with enough precision to guide independent agents to consistent decisions.

The fix is a single document that encodes every non-negotiable before the first prompt: your stack, your accuracy policy, your TDD requirements, your branching and PR conventions, your definition of what "done" means. In my projects this is `DEVELOPMENT.md`, committed to the repository, read by every agent at session start.

For repo-pulse, DEVELOPMENT.md specifies that all data must come from the GitHub GraphQL API, that the app is stateless with no database, that every feature must include tests before implementation begins, and that the health scoring algorithm is the authoritative logic that no agent may alter without an explicit spec. Agents do not decide these things. I decided them once. The document enforces them forever.

The companion document is `PRODUCT.md`, which defines what the product is and is not. When an agent proposes a feature that sounds reasonable but falls outside the product scope, PRODUCT.md is the answer, not a conversation.

Write these documents before you start. They are the highest-leverage thing you will do all month.

---

## A: Agnostic Core

The AI tooling landscape is moving faster than any production system can safely track. The model that is best today may not be best in six months. The git provider you use may change its API pricing. The specification methodology your team adopts may be superseded by something better.

Build your workflow so that none of those changes require you to rebuild your process.

In agentctl, this means the agent is selected at runtime with `--agent claude` or `--agent codex`, and adapters are YAML files that live outside the binary. Switching models is a one-line change. In my projects, I swapped models frequently — Claude for deep analysis, Codex for implementation, a second Claude session for verification — and this cross-model workflow had an unexpected benefit: different models hallucinate differently. Running the same codebase through multiple models at different stages of the workflow is a practical way to surface hallucinations that a single-model workflow would miss entirely.

The same principle applies to specification methodology. Some features in repo-pulse use SpecKit, a structured spec-then-plan-then-tasks approach. Others use plain prose specs. Some use no formal spec at all, just a well-written GitHub issue. The workflow accommodates all of them because it does not mandate any of them. Other SDD methodologies are on the roadmap: [AgentOS-style specs](https://github.com/arun-gupta/agentctl/issues/35) and [Kiro-style specs](https://github.com/arun-gupta/agentctl/issues/39) are both planned additions. The human decides which technique is appropriate for the complexity of the task.

Stay portable. Your future self will thank you every time a vendor announces a price change.

---

## T: Trio

A single agent wearing all the hats is a single point of failure. The same model that writes the code also decides whether the code is correct. That is not verification, it is self-review, and it has all the same blind spots.

The pattern that works is a three-agent cycle with distinct roles: one agent for analysis and specification, a second agent to implement, and a third (or the first again) to verify. The separation is the point. An agent reviewing code it did not write catches different things than the agent that wrote it. That said, in practice the roles can and do collapse — particularly analysis and implementation, where the overhead of a separate session is not always worth it for smaller changes. What I found to be genuinely non-negotiable was keeping verification and PR review separate: a different model for the final check than the one that did the building.

In practice for repo-pulse: I used a Claude session to read the GitHub issue, analyse the codebase, and produce a spec — but a formal spec was only generated for major features; smaller changes went straight to implementation with a well-written issue as the guide. A second session implemented against that spec. Copilot then reviewed the PR, checking that the implementation matched the spec and that no regressions were introduced. The pattern of Claude handling analysis and implementation while Copilot reviewed the PR became a consistent workflow precisely because the two models have different blind spots — what one misses, the other tends to catch. This is not theoretical overhead. It catches real bugs, particularly around edge cases the implementing agent optimistically assumed away.

The three-agent cycle does not have to be three separate model calls. It is three separate jobs with three separate prompts, three separate contexts, and three separate outputs. How you schedule them is up to you.

---

## F: Forced Gates

The most common mistake in AI-assisted development is putting humans in the loop at the wrong points. Teams review every line of generated code, approve every commit, and supervise every agent session. The result is that AI saves the typing but not the time.

The alternative is to identify the two or three decisions in your workflow that carry the most risk and gate humans there, nowhere else.

For me those gates are spec approval and merge — and these two are non-negotiable. The spec is the contract between the human and the agent: this is what we are building, this is how we will know it is done. A human approves the spec before any implementation begins. The merge is the final gate: a human reviews the diff, runs the PR test plan, and decides whether the work meets the standard. Everything between those two gates runs autonomously.

In agentctl, the headless mode exists precisely for this reason. The agent runs in the background, appends to a log, and notifies on completion. The human is not present for the session. They are present for the spec and for the merge. That is enough.

Forced gates work because they are forced. If the gate can be bypassed under pressure, it will be bypassed under pressure. The branch protection rule and the PR review requirement are not bureaucracy. They are the mechanism.

---

## O: Orchestrated DoD

Every team has a definition of done. Almost no team enforces it consistently, because enforcement is manual and manual work gets skipped when deadlines tighten.

Automate it. Build a subagent whose only job is to verify that a PR meets the definition of done before a human reviewer spends any time on it.

For repo-pulse, this is the [`dod-verifier`](https://github.com/arun-gupta/repo-pulse/blob/main/.claude/agents/dod-verifier.md) — a Claude Code sub-agent running on the haiku model that mechanically walks the project's Definition of Done before any PR is marked ready for review. It checks nine criteria:

- Acceptance criteria satisfied (flagged for human sign-off)
- Tests passing
- Lint clean
- Build succeeding
- No TODOs or `console.log` statements in changed files
- PR body containing a test plan
- README updated for user-facing changes
- DEVELOPMENT.md implementation-order row updated
- Constitution compliance (flagged for human sign-off)

Each check reports `SATISFIED`, `BLOCKED` with evidence, or `REQUIRES HUMAN SIGN-OFF`. The subagent never edits source files — its role is verification only.

A human reviewer never sees a PR that fails any of the mechanical checks. By the time a human looks at the code, the automatable questions are already answered. They can focus on the things that require judgment: architecture, naming, edge cases the tests do not cover.

The subagent is not optional and it is not advisory. A PR that gets a BLOCKED verdict from the DoD agent does not move forward until the agent is satisfied. That is what "forced" means.

---

## R: Rhythm

Tech debt does not accumulate dramatically. It accumulates one small compromise at a time, each one individually reasonable, collectively catastrophic. The quarterly tech debt sprint exists because teams let it build for three months and then spend a week cleaning it up. That cycle is expensive and demoralizing.

The goal is a daily rhythm: a scheduled agent that scans for linting violations, deprecated dependencies, test coverage gaps, and TODO comments older than a defined threshold, and opens a small PR for each class of issue it finds. Each PR is small. Each is easy to review. None of them requires a sprint. Building the right mechanisms to catch this technical debt early — before it compounds — is one of the areas I am still actively working on.

The rule is: if you would not accept this condition in a codebase you were reviewing, do not let it persist for more than 24 hours in your own.

---

## M: Mirror

The agent that wrote the code has a model of what the code does. That model may be wrong in ways the agent cannot detect, because the agent's blind spots are structural, not random. A different model, reading the same code without the context of having written it, will catch different things.

The practice is simple: after an agent completes a PR, route it to a different model for async review. The reviewer reads the diff, the spec, and the test plan, and posts comments as a GitHub review. It does not block the PR automatically. It informs the human reviewer.

In my workflow, this creates a useful division of labour. The DoD subagent checks mechanical correctness: do the tests pass, does the coverage meet the threshold, does the build succeed. The mirror review checks semantic correctness: does this implementation actually do what the spec asked for, are there edge cases the tests do not cover, is this the right abstraction.

Two different models, two different perspectives, before a human spends significant time on review. The human's job is to arbitrate between the two and make the final call. That is a much more tractable job than reviewing a 400-line diff cold.

---

## The Framework in Practice

These eight patterns are not independent. They reinforce each other.

The Law of the Land (L) makes the Per-Feature Sandbox (P) safe, because every agent in every worktree starts from the same constraints. The Trio (T) works because the Forced Gates (F) ensure a spec exists before implementation begins. The Orchestrated DoD (O) is only possible because the Law of the Land defines what done means. The Rhythm (R) only stays lightweight because the Forced Gates prevent large accumulations of unchecked work from merging. The Mirror (M) is only actionable because the spec provides a reference for what the implementation should do.

Build them in order. Start with L and P. Add F when your first agent completes its first PR. Add T and O when you have enough throughput that manual verification becomes the bottleneck. Add R and M last, when the foundation is stable enough that incremental quality improvements compound.

The PLATFORM framework will not make AI agents write perfect code. Nothing will. What it will do is make their output predictable, reviewable, and correctable, which is the actual goal. Perfect code is a fantasy. Shippable code on a reliable cadence is a business.

---

agentctl and repo-pulse are both open source. If you want to start with the tooling rather than building it yourself, both are worth a look. The patterns, though, are the transferable thing. They work with any agent, any provider, and any methodology, because they are not about the tools. They are about who owns which decisions and when.

That is the part that survives vendor shifts.
