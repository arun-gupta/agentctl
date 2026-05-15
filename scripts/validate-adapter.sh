#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: validate-adapter.sh <adapter-name> [--issue N] [--repo-path PATH]" >&2
}

if [[ $# -lt 1 || "${1:-}" == --* ]]; then
  usage
  exit 1
fi

ADAPTER="$1"
REPO_PATH="../agentctl-test"
ISSUE=""
WORKTREE=""
BRANCH=""
PASS=0
FAIL=0
MANUAL=3

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
    *)
      echo "Unknown flag: $1" >&2
      usage
      exit 1
      ;;
  esac
done

pass() {
  echo "[PASS] $*"
  PASS=$((PASS + 1))
}

fail() {
  echo "[FAIL] $*"
  FAIL=$((FAIL + 1))
}

skip() {
  echo "[SKIP] $* - manual check required"
}

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

adapter_binary() {
  agentctl info 2>&1 | awk -v adapter="$ADAPTER" '
    $0 == "  " adapter { found=1; next }
    found && $0 ~ /^  [^ ]/ { found=0 }
    found && $0 ~ /^[[:space:]]+binary:/ {
      sub(/^[[:space:]]+binary:[[:space:]]*/, "", $0)
      print $0
      exit
    }
  '
}

resolve_worktree() {
  local record path branch
  while IFS= read -r record; do
    case "$record" in
      worktree\ *)
        path="${record#worktree }"
        ;;
      branch\ refs/heads/*)
        branch="${record#branch refs/heads/}"
        if [[ "$branch" == "${ISSUE}-"* ]]; then
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

if agentctl info 2>&1 | grep -q "$ADAPTER"; then
  pass "agentctl info lists adapter \"$ADAPTER\""
else
  fail "agentctl info does not list adapter \"$ADAPTER\""
fi

BINARY="$(adapter_binary || true)"
BINARY_CMD="${BINARY%% *}"
if [[ -n "$BINARY_CMD" ]] && BINARY_PATH="$(command -v "$BINARY_CMD" 2>/dev/null)"; then
  BINARY_BAK="${BINARY_PATH}.validate-bak"
  mv "$BINARY_PATH" "$BINARY_BAK"
  OUTPUT="$(cd "$REPO_PATH" && agentctl agent start --agent "$ADAPTER" "$ISSUE" 2>&1 || true)"
  mv "$BINARY_BAK" "$BINARY_PATH"
  if grep -qi "install" <<<"$OUTPUT"; then
    pass "install hint shown when binary is missing"
  else
    fail "no install hint shown when binary is missing"
  fi
else
  skip "install hint check - binary not on PATH, skipping hide-and-restore"
fi

START="$(date +%s)"

if (cd "$REPO_PATH" && agentctl agent start --agent "$ADAPTER" --headless "$ISSUE"); then
  pass "agentctl agent start launched agent"
else
  fail "agentctl agent start failed - aborting smoke test"
  exit 1
fi

if resolve_worktree; then
  :
else
  fail "could not resolve worktree path for issue #$ISSUE"
fi

if (cd "$REPO_PATH" && agentctl agent status 2>&1 | grep -q "$ISSUE"); then
  pass "agentctl agent status shows issue #$ISSUE"
else
  fail "agentctl agent status does not show issue #$ISSUE"
fi

sleep 2
if (cd "$REPO_PATH" && agentctl agent logs "$ISSUE" --no-follow --lines 5 2>&1 | grep -q .); then
  pass "agentctl agent logs produces output"
else
  fail "agentctl agent logs produced no output"
fi

if (cd "$REPO_PATH" && agentctl agent attach "$ISSUE"); then
  ELAPSED="$(( $(date +%s) - START ))"
  pass "agent exited cleanly in ${ELAPSED}s"
else
  fail "agent exited with non-zero status"
fi

if [[ -n "$WORKTREE" ]] && CHANGED="$(changed_files_stat)" && grep -q . <<<"$CHANGED"; then
  pass "files changed in worktree (${CHANGED##*$'\n'})"
else
  fail "no file changes detected in worktree at $WORKTREE"
fi

skip "resume: cd $REPO_PATH && agentctl agent resume $ISSUE 'revise the implementation'"
skip "notify: agentctl agent start --agent $ADAPTER --headless --notify $ISSUE"
skip "full lifecycle: verify PR opened, agentctl worktree merge, agentctl report"

echo
echo "Result: $PASS automated checks passed, $FAIL failed, $MANUAL manual checks pending"
[[ $FAIL -eq 0 ]]
