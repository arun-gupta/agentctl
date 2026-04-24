#!/usr/bin/env bash
# Google Gemini CLI adapter for agentctl
# Implements: agent_launch, agent_resume, agent_pause_state
#
# Requires the Gemini CLI (https://github.com/google-gemini/gemini-cli).
# Install: npm install -g @google/gemini-cli
# Binary:  gemini
# Auth:    GEMINI_API_KEY environment variable (or interactive Google login)
#
# Non-interactive prompt injection uses the -p / --prompt flag.
# Session continuity records the session-id in $wt/.agent for reference;
# resume sends the new prompt with -p, continuing work in the same worktree.

agent_launch() {
  local wt="$1" issue="$2" _port="$3" session_id="$4" kickoff="$5" headless="$6"

  cd "$wt" || exit 1
  if (( headless )); then
    nohup gemini -p "$kickoff" > agent.log 2>&1 &
    local pid=$!
    echo "agent-pid=$pid" >> ".agent"
    echo "Gemini (headless) PID $pid — log: $wt/agent.log"
    echo "Session ID: $session_id (recorded in $wt/.agent)"
    echo "Release the pause with: agentctl approve-spec $issue"
  else
    exec gemini -p "$kickoff"
  fi
}

agent_resume() {
  local wt="$1" prompt="$2"
  local session_id
  session_id="$(grep '^session-id=' "$wt/.agent" 2>/dev/null | head -1 | cut -d= -f2- || true)"
  if [[ -z "${session_id:-}" ]]; then
    echo "No session ID recorded; cannot resume non-interactively." >&2
    echo "Use 'cd $wt && gemini' to continue the session manually." >&2
    exit 1
  fi
  ( cd "$wt" && nohup gemini -p "$prompt" >> agent.log 2>&1 & )
}

agent_pause_state() {
  local wt="$1" issue="$2"
  local state="no-spec"
  if [[ -n "${issue:-}" ]] && compgen -G "$wt/specs/${issue}-*/spec.md" > /dev/null 2>&1; then
    if compgen -G "$wt/specs/${issue}-*/tasks.md" > /dev/null 2>&1; then
      state="done"
    elif compgen -G "$wt/specs/${issue}-*/plan.md" > /dev/null 2>&1; then
      state="in-progress"
    else
      state="paused"
    fi
  fi
  echo "$state"
}
