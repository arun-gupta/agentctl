#!/usr/bin/env bash
# GitHub Copilot CLI adapter for agentctl
# Implements: agent_launch, agent_resume
#
# Requires the GitHub Copilot CLI (https://github.com/features/copilot/cli).
# Install: npm install -g @github/copilot
# Binary:  copilot
# Auth:    GITHUB_TOKEN environment variable or interactive 'copilot auth'
#
# Non-interactive prompt injection uses the -p / --prompt flag.
# Session continuity records the session-id in $wt/.agent and passes it via
# --session-id on subsequent invocations, mirroring the claude adapter pattern.
#
# Resume limitation: GitHub Copilot CLI session resume depends on the CLI
# version supporting --session-id on invocation. If not supported, use
# 'cd <worktree> && copilot' to continue the session manually.

agent_launch() {
  local wt="$1" _issue="$2" _port="$3" session_id="$4" kickoff="$5"
  cd "$wt" || exit 1
  nohup copilot -p "$kickoff" --session-id "$session_id" > agent.log 2>&1 &
  printf 'agent-pid=%s\nsession-id=%s\n' "$!" "$session_id" > .agent
}

agent_resume() {
  local wt="$1" prompt="$2"
  local session_id
  session_id="$(grep '^session-id=' "$wt/.agent" 2>/dev/null | head -1 | cut -d= -f2- || true)"
  if [[ -z "${session_id:-}" ]]; then
    echo "No session ID recorded; cannot resume non-interactively." >&2
    echo "Use 'cd $wt && copilot' to continue the session manually." >&2
    exit 1
  fi
  ( cd "$wt" && nohup copilot -p "$prompt" --session-id "$session_id" >> agent.log 2>&1 & )
}
