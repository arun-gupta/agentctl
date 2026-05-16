#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: validate-adapter.sh <adapter-name> [--issue N] [--repo-path PATH] [--headless] [--agentctl-bin PATH]" >&2
}

if [[ $# -lt 1 || "${1:-}" == --* ]]; then
  usage
  exit 1
fi

ADAPTER="$1"
REPO_PATH="../agentctl-test"
ISSUE=""
HEADLESS=false
AGENTCTL="agentctl"
WORKTREE=""
BRANCH=""
WORKTREES_BEFORE=""
PASS=0
FAIL=0

shift
while [[ $# -gt 0 ]]; do
  case "$1" in
    --issue)
      [[ $# -ge 2 ]] || { echo "missing value for --issue" >&2; exit 1; }
      ISSUE="$2"
      shift 2
      ;;
    --repo-path)
      [[ $# -ge 2 ]] || { echo "missing value for --repo-path" >&2; exit 1; }
      REPO_PATH="$2"
      shift 2
      ;;
    --headless)
      HEADLESS=true
      shift
      ;;
    --agentctl-bin)
      [[ $# -ge 2 ]] || { echo "missing value for --agentctl-bin" >&2; exit 1; }
      AGENTCTL="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
      shift 2
      ;;
    *)
      echo "Unknown flag: $1" >&2
      usage
      exit 1
      ;;
  esac
done

pass() { echo "[PASS] $*"; PASS=$((PASS + 1)); }
fail() { echo "[FAIL] $*"; FAIL=$((FAIL + 1)); }

cleanup() {
  if [[ -n "$WORKTREE" && -d "$WORKTREE" ]]; then
    git -C "$REPO_PATH" worktree remove --force "$WORKTREE" >/dev/null 2>&1 || true
  fi
  if [[ -n "$BRANCH" ]]; then
    git -C "$REPO_PATH" branch -D "$BRANCH" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

resolve_issue() {
  if [[ -n "$ISSUE" ]]; then
    return
  fi

  ISSUE="$(gh issue list --repo arun-gupta/agentctl-test --limit 1 --json number -q '.[0].number')"
  if [[ -z "$ISSUE" || "$ISSUE" == "null" ]]; then
    echo "failed to resolve an open issue from arun-gupta/agentctl-test" >&2
    exit 1
  fi
}

# Extract only the "Coding Agents:" block from agentctl info output.
# Stops at the first non-indented line after the block starts, preventing
# false positives from config lines like "default_agent: gemini".
coding_agents_section() {
  "$AGENTCTL" info 2>&1 | awk '/^Coding Agents:/{found=1;next} found && /^[^[:space:]]/{found=0} found{print}'
}

# Resolve the worktree created by agent start for ISSUE by diffing
# git worktree list before and after the start call. This prevents
# accidentally selecting a pre-existing worktree whose branch already
# matches ${ISSUE}-*.
resolve_worktree() {
  local record path branch
  while IFS= read -r record; do
    case "$record" in
      worktree\ *)
        path="${record#worktree }"
        ;;
      branch\ refs/heads/*)
        branch="${record#branch refs/heads/}"
        if [[ "$branch" == "${ISSUE}-"* ]] && ! grep -qxF "worktree $path" <<<"$WORKTREES_BEFORE"; then
          WORKTREE="$path"
          BRANCH="$branch"
          return 0
        fi
        ;;
    esac
  done < <(git -C "$REPO_PATH" worktree list --porcelain)
  return 1
}

changed_files_stat() {
  if git -C "$WORKTREE" rev-parse HEAD~1 >/dev/null 2>&1; then
    git -C "$WORKTREE" diff --stat HEAD~1 2>/dev/null
    return
  fi

  git -C "$WORKTREE" status --short 2>/dev/null
}

resolve_issue

echo "[validate-adapter] adapter: $ADAPTER  repo: arun-gupta/agentctl-test  issue: #$ISSUE"

# ── Level 1: Binary & wiring ─────────────────────────────────────────────

AGENTS_SECTION="$(coding_agents_section)"

# 1a: adapter appears in Coding Agents section — anchored to the adapter name
# token to avoid matching config lines elsewhere in agentctl info output.
if echo "$AGENTS_SECTION" | grep -qE "[[:space:]]${ADAPTER}([[:space:]]|$)"; then
  pass "agentctl info lists adapter \"$ADAPTER\""
else
  fail "agentctl info does not list adapter \"$ADAPTER\""
fi

# 1b: install hint present when adapter binary is not installed.
# Reads directly from agentctl info output — no binary rename or PATH mutation needed.
ADAPTER_LINE="$(echo "$AGENTS_SECTION" | grep -E "[[:space:]]${ADAPTER}([[:space:]]|$)" | head -1)"
if echo "$ADAPTER_LINE" | grep -q "not installed" && echo "$ADAPTER_LINE" | grep -q "install with:"; then
  pass "install hint shown when binary is missing"
elif echo "$ADAPTER_LINE" | grep -q "not installed"; then
  fail "adapter binary not installed but no install hint provided in agentctl info"
else
  echo "[INFO] install hint not checked — binary is installed; test manually by temporarily removing it from PATH"
fi

# ── Level 2: Launch smoke test ───────────────────────────────────────────

START="$(date +%s)"

# Snapshot existing worktrees before agent start so resolve_worktree can
# identify the newly-created one without clobbering a pre-existing worktree.
WORKTREES_BEFORE="$(git -C "$REPO_PATH" worktree list --porcelain)"

START_ARGS=(--agent "$ADAPTER")
$HEADLESS && START_ARGS+=(--headless)

if (cd "$REPO_PATH" && "$AGENTCTL" agent start "${START_ARGS[@]}" "$ISSUE"); then
  if $HEADLESS; then
    pass "agentctl agent start launched agent (headless)"
  else
    ELAPSED="$(( $(date +%s) - START ))"
    pass "agent exited cleanly in ${ELAPSED}s"
  fi
else
  fail "agentctl agent start failed - aborting smoke test"
  exit 1
fi

if resolve_worktree; then
  :
else
  fail "could not resolve worktree path for issue #$ISSUE"
fi

# Match exact first column in agent status table to avoid false positives
# where issue number "42" would match row "142".
if (cd "$REPO_PATH" && "$AGENTCTL" agent status 2>&1 | awk -v issue="$ISSUE" 'NR>1 && $1==issue{found=1} END{exit !found}'); then
  pass "agentctl agent status shows issue #$ISSUE"
else
  fail "agentctl agent status does not show issue #$ISSUE"
fi

# agentctl agent logs reads from agent.log, which only exists in headless mode.
if $HEADLESS; then
  sleep 2
  if (cd "$REPO_PATH" && "$AGENTCTL" agent logs "$ISSUE" --no-follow --lines 5 2>&1 | grep -q .); then
    pass "agentctl agent logs produces output"
  else
    fail "agentctl agent logs produced no output"
  fi
  if (cd "$REPO_PATH" && "$AGENTCTL" agent attach "$ISSUE"); then
    ELAPSED="$(( $(date +%s) - START ))"
    pass "agent exited cleanly in ${ELAPSED}s"
  else
    fail "agent exited with non-zero status"
  fi
else
  echo "[INFO] agentctl agent logs not checked (headless only)"
fi

if [[ -n "$WORKTREE" ]] && CHANGED="$(changed_files_stat)" && grep -q . <<<"$CHANGED"; then
  pass "files changed in worktree (${CHANGED##*$'\n'})"
else
  fail "no file changes detected in worktree at $WORKTREE"
fi

# PR check — any open PR whose branch starts with the issue number covers both
# agentctl-named branches and agent self-named branches (e.g. opencode).
PR_URL="$(cd "$REPO_PATH" && gh pr list --json headRefName,url -q ".[] | select(.headRefName | startswith(\"${ISSUE}-\")) | .url" 2>/dev/null | head -1 || true)"
if [[ -n "$PR_URL" ]]; then
  pass "PR opened: $PR_URL"
else
  fail "no PR opened for issue #$ISSUE"
fi

echo
echo "Result: $PASS automated checks passed, $FAIL failed"
echo "Manual checks: resume, notify, and full lifecycle (PR open → merge → report) require a separate run."
[[ $FAIL -eq 0 ]]
