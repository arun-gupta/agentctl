#!/usr/bin/env bash
# GitHub Copilot adapter for scripts/agent.sh — stub implementation
# Implements: agent_launch, agent_resume, agent_pause_state
#
# agent_launch and agent_resume are stubs that exit non-zero.
# Replace with real `gh copilot` invocations when the CLI supports
# non-interactive session launch and resume.

agent_launch() {
  local wt="$1" issue="$2" port="$3" session_id="$4" kickoff="$5" headless="$6"
  echo "copilot adapter: agent_launch is not yet implemented." >&2
  echo "To implement: invoke 'gh copilot' with the kickoff prompt and" >&2
  echo "append 'agent-pid=<pid>' to $wt/.agent once the process is running." >&2
  exit 1
}

agent_resume() {
  local wt="$1" prompt="$2"
  echo "copilot adapter: agent_resume is not yet implemented." >&2
  echo "To implement: resume the Copilot session using whatever state is" >&2
  echo "recorded in $wt/.agent and append to $wt/agent.log." >&2
  exit 1
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
