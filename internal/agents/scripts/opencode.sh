#!/usr/bin/env bash
# OpenCode CLI adapter for agentctl
# Implements: agent_launch, agent_resume
#
# Requires the OpenCode CLI (https://opencode.ai).
# Install: npm install -g opencode@latest
# Binary:  opencode
# Auth:    configure via 'opencode auth' or set provider API keys
#
# Non-interactive prompt injection uses the 'run' subcommand with -p / --print.
# Session continuity records the session-id in $wt/.agent and passes it via
# --session on subsequent invocations, mirroring the claude adapter pattern.

agent_launch() {
  local wt="$1" issue="$2" _port="$3" session_id="$4" kickoff="$5" headless="$6"

  cd "$wt" || exit 1
  if (( headless )); then
    nohup opencode run -p "$kickoff" --session "$session_id" > agent.log 2>&1 &
    local pid=$!
    echo "agent-pid=$pid" >> ".agent"
    echo "OpenCode (headless) PID $pid — log: $wt/agent.log"
    echo "Session ID: $session_id (recorded in $wt/.agent)"
    echo "Release the pause with: agentctl approve-spec $issue"
  else
    exec opencode run -p "$kickoff" --session "$session_id"
  fi
}

agent_resume() {
  local wt="$1" prompt="$2"
  local session_id
  session_id="$(grep '^session-id=' "$wt/.agent" 2>/dev/null | head -1 | cut -d= -f2- || true)"
  if [[ -z "${session_id:-}" ]]; then
    echo "No session ID recorded; cannot resume non-interactively." >&2
    echo "Use 'cd $wt && opencode' to continue the session manually." >&2
    exit 1
  fi
  ( cd "$wt" && nohup opencode run -p "$prompt" --session "$session_id" >> agent.log 2>&1 & )
}
