#!/usr/bin/env bash
# Claude Code adapter for scripts/agent.sh
# Implements: agent_launch, agent_resume, agent_pause_state

agent_launch() {
  local wt="$1" issue="$2" port="$3" session_id="$4" kickoff="$5" headless="$6"

  cd "$wt"
  if (( headless )); then
    nohup claude -p "$kickoff" --session-id "$session_id" > agent.log 2>&1 &
    local pid=$!
    echo "agent-pid=$pid" >> ".agent"
    echo "Claude (headless) PID $pid — log: $wt/agent.log"
    echo "Session ID: $session_id (recorded in $wt/.agent)"
    echo "Release the pause with: scripts/agent.sh --approve-spec $issue"
  else
    exec claude --session-id "$session_id" "$kickoff"
  fi
}

agent_resume() {
  local wt="$1" prompt="$2"
  local session_id
  session_id="$(grep '^session-id=' "$wt/.agent" 2>/dev/null | head -1 | cut -d= -f2- || true)"
  if [[ -z "${session_id:-}" ]]; then
    echo "No session ID recorded; cannot resume non-interactively." >&2
    echo "Use 'cd $wt && claude --resume' instead." >&2
    exit 1
  fi
  ( cd "$wt" && nohup claude -p "$prompt" --resume "$session_id" >> agent.log 2>&1 & )
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
