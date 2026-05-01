# Build Your PLATFORM: 8 Patterns for AI-Assisted Development That Survive Vendor Shifts

Everyone is experimenting with AI coding agents. Far fewer are shipping production software with them reliably.

The gap is not the models. The models are remarkable. The gap is the workflow around them. Most teams treat AI agents like a faster keyboard, drop them into an existing process, and wonder why the output requires as much correction as it saves in effort. Context drifts. Agents hallucinate dependencies. One agent undoes what another just wrote. And code review becomes harder, not easier, because the diffs are larger and the reviewer has less context than the agent did.

Over the past year, we built two open-source projects using AI agents as first-class contributors: agentctl, a CLI that orchestrates coding agents across isolated git worktrees, and repo-pulse, a repository health analytics platform built with Next.js. Neither project would exist in its current form without AI agents. Neither would have shipped reliably without the framework we developed to govern them.

We call it PLATFORM. Eight patterns, one per letter, that together turn AI agents from expensive autocomplete into a genuine force multiplier. The framework is deliberately agnostic: it works with any model, any git provider, and any specification methodology, because the vendor landscape of 2026 will not look like 2027.

---

## P: Per-Feature Sandbox

The first mistake most teams make is sharing branches between agents and humans, or between multiple agents running in parallel. Shared state means shared risk. One agent rebasing at the wrong moment corrupts another's work. A human pushing a fix lands on top of an in-progress agent session.

The solution is simple: one git worktree per feature, per agent, per task. Not branches on the same checkout, but fully isolated worktrees that can be discarded without affecting anything else. When an agent session goes wrong, you throw the worktree away. No cleanup, no rebase, no untangling.

In agentctl, this is the core primitive. Every `agentctl start` command creates a linked worktree at `../<repo>-<issue>-<slug>/`, provisions the environment, and writes a `.agent` metadata file. When the work is done, `agentctl cleanup` removes it. The main worktree never sees the mess.

For repo-pulse, we ran multiple feature agents in parallel, each in its own worktree, each working on a separate GitHub issue. None of them could interfere with each other. When two agents happened to touch the same utility function, the conflict surfaced at PR review time, not mid-session, and was resolved once.

The pattern costs you nothing except disk space.

---

## L: Law of the Land

Agents are eager to help and have no memory of your last conversation. Without a written source of truth, every session starts from scratch: the agent infers your stack from the files it can see, guesses your testing philosophy from the existing tests, and makes decisions about accuracy, security, and workflow that you never explicitly endorsed.

The fix is a single document that encodes every non-negotiable before the first prompt: your stack, your accuracy policy, your TDD requirements, your branching and PR conventions, your definition of what "done" means. In our projects this is `DEVELOPMENT.md`, committed to the repository, read by every agent at session start.

For repo-pulse, DEVELOPMENT.md specifies that all data must come from the GitHub GraphQL API, that the app is stateless with no database, that every feature must include tests before implementation begins, and that the health scoring algorithm is the authoritative logic that no agent may alter without an explicit spec. Agents do not decide these things. We decided them once. The document enforces them forever.

The companion document is `PRODUCT.md`, which defines what the product is and is not. When an agent proposes a feature that sounds reasonable but falls outside the product scope, PRODUCT.md is the answer, not a conversation.

Write these documents before you start. They are the highest-leverage thing you will do all month.

---

## A: Agnostic Core

The AI tooling landscape is moving faster than any production system can safely track. The model that is best today may not be best in six months. The git provider you use may change its API pricing. The specification methodology your team adopts may be superseded by something better.

Build your workflow so that none of those changes require you to rebuild your process.

In agentctl, this means the agent is selected at runtime with `--agent claude` or `--agent codex`, and adapters are YAML files that live outside the binary. Switching models is a one-line change. In our projects, we have run Claude for deep analysis, Codex for implementation, and a second Claude session for verification, all within the same workflow, all governed by the same `DEVELOPMENT.md`.

The same principle applies to specification methodology. Some features in repo-pulse use SpecKit, a structured spec-then-plan-then-tasks approach. Others use plain prose specs. Some use no formal spec at all, just a well-written GitHub issue. The workflow accommodates all of them because it does not mandate any of them. The human decides which technique is appropriate for the complexity of the task.

Stay portable. Your future self will thank you every time a vendor announces a price change.

---

## T: Trio

A single agent wearing all the hats is a single point of failure. The same model that writes the code also decides whether the code is correct. That is not verification, it is self-review, and it has all the same blind spots.

The pattern that works is a three-agent cycle with distinct roles: one agent for analysis and specification, a second agent to implement, and the first agent (or a different one) to verify. The separation is the point. An agent reviewing code it did not write catches different things than the agent that wrote it.

In practice for repo-pulse: we use a Claude session to read the GitHub issue, analyse the codebase, and produce a spec. A second session implements against that spec. The first session then reviews the diff, checking that the implementation matches the spec and that no regressions were introduced. This is not theoretical overhead. It catches real bugs, particularly around edge cases the implementing agent optimistically assumed away.

The three-agent cycle does not have to be three separate model calls. It is three separate jobs with three separate prompts, three separate contexts, and three separate outputs. How you schedule them is up to you.

---

## F: Forced Gates

The most common mistake in AI-assisted development is putting humans in the loop at the wrong points. Teams review every line of generated code, approve every commit, and supervise every agent session. The result is that AI saves the typing but not the time.

The alternative is to identify the two or three decisions in your workflow that carry the most risk and gate humans there, nowhere else.

For us those gates are spec approval and merge. The spec is the contract between the human and the agent: this is what we are building, this is how we will know it is done. A human approves the spec before any implementation begins. The merge is the final gate: a human reviews the diff, runs the PR test plan, and decides whether the work meets the standard. Everything between those two gates runs autonomously.

In agentctl, the headless mode exists precisely for this reason. The agent runs in the background, appends to a log, and notifies on completion. The human is not present for the session. They are present for the spec and for the merge. That is enough.

Forced gates work because they are forced. If the gate can be bypassed under pressure, it will be bypassed under pressure. The branch protection rule and the PR review requirement are not bureaucracy. They are the mechanism.

---

## O: Orchestrated DoD

Every team has a definition of done. Almost no team enforces it consistently, because enforcement is manual and manual work gets skipped when deadlines tighten.

Automate it. Build a subagent whose only job is to verify that a PR meets the definition of done before a human reviewer spends any time on it. In our workflow, this is the PR test runner: a Claude Code agent that reads the test plan from the PR description, executes every automatable check, marks each checkbox as it completes, and posts a summary comment with an overall READY or BLOCKED verdict.

For repo-pulse, the DoD subagent checks that the implementation matches the spec, that all tests pass, that the coverage threshold is met, that the accessibility audit passes, and that the bundle size is within budget. A human reviewer never sees a PR that fails any of these checks. By the time a human looks at the code, the mechanical questions are already answered. They can focus on the things that require judgment: architecture, naming, edge cases the tests do not cover.

The subagent is not optional and it is not advisory. A PR that gets a BLOCKED verdict from the DoD agent does not move forward until the agent is satisfied. That is what "forced" means.

---

## R: Rhythm

Tech debt does not accumulate dramatically. It accumulates one small compromise at a time, each one individually reasonable, collectively catastrophic. The quarterly tech debt sprint exists because teams let it build for three months and then spend a week cleaning it up. That cycle is expensive and demoralizing.

The alternative is a daily rhythm. A scheduled agent runs every morning, scans for linting violations, deprecated dependencies, test coverage gaps, and TODO comments older than a defined threshold, and opens a single small PR for each class of issue it finds. Each PR is small. Each is easy to review. None of them requires a sprint.

In repo-pulse, the daily tech debt runner also monitors the health score of the repository itself, using the same scoring logic the product exposes to its users. When our own activity score drops, or our mean issue response time creeps up, the runner flags it. We eat our own cooking.

The rule is: if you would not accept this condition in a codebase you were reviewing, do not let it persist for more than 24 hours in your own.

---

## M: Mirror

The agent that wrote the code has a model of what the code does. That model may be wrong in ways the agent cannot detect, because the agent's blind spots are structural, not random. A different model, reading the same code without the context of having written it, will catch different things.

The practice is simple: after an agent completes a PR, route it to a different model for async review. The reviewer reads the diff, the spec, and the test plan, and posts comments as a GitHub review. It does not block the PR automatically. It informs the human reviewer.

In our workflow, this creates a useful division of labour. The DoD subagent checks mechanical correctness: do the tests pass, does the coverage meet the threshold, does the build succeed. The mirror review checks semantic correctness: does this implementation actually do what the spec asked for, are there edge cases the tests do not cover, is this the right abstraction.

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
