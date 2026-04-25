#!/usr/bin/env bash
# OpenAI Codex CLI adapter for agentctl
# Implements: agent_launch, agent_resume
#
# Requires the Codex CLI (https://github.com/openai/codex).
# Install: npm install -g @openai/codex
# Binary:  codex
#
# Non-interactive prompt injection uses the -q / --quiet flag.
# Session continuity uses --session <session-id> on launch and
# --resume <session-id> on subsequent invocations, mirroring the
# claude adapter pattern.

agent_launch() {
  local wt="$1" _issue="$2" _port="$3" session_id="$4" kickoff="$5"
  cd "$wt" || exit 1
  nohup codex -q "$kickoff" --session "$session_id" > agent.log 2>&1 &
  printf 'agent-pid=%s\nsession-id=%s\n' "$!" "$session_id" > .agent
}

agent_resume() {
  local wt="$1" prompt="$2"
  local session_id
  session_id="$(grep '^session-id=' "$wt/.agent" 2>/dev/null | head -1 | cut -d= -f2- || true)"
  if [[ -z "${session_id:-}" ]]; then
    echo "No session ID recorded; cannot resume non-interactively." >&2
    echo "Use 'cd $wt && codex --resume' instead." >&2
    exit 1
  fi
  ( cd "$wt" && nohup codex -q "$prompt" --resume "$session_id" >> agent.log 2>&1 & )
}
