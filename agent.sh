#!/usr/bin/env bash
# Provision an isolated agent worktree for an issue and launch a coding agent in it.
# Run `scripts/agent.sh --help` for usage.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

print_usage() {
  cat <<'EOF'
Provision an isolated agent worktree for an issue and launch a coding agent in it.

Usage:
  scripts/agent.sh [--agent <name>] [--headless] [--no-speckit] <issue-number> [slug]
  scripts/agent.sh --approve-spec        <issue-number>
  scripts/agent.sh --revise-spec         <issue-number> <feedback>
  scripts/agent.sh --discard             [<issue-number>]
  scripts/agent.sh --cleanup-merged      [<issue-number>]
  scripts/agent.sh --cleanup-all-merged
  scripts/agent.sh --status

Options:
  --agent <name>         Select coding agent adapter (default: claude).
  --headless             Run agent in background (log -> agent.log)
  --no-speckit           Skip SpecKit lifecycle; agent opens a PR directly (no spec pause)
  --approve-spec         Release the spec-review pause for a paused headless spawn
  --revise-spec          Send non-empty revision feedback to a paused spawn
  --discard              Discard worktree + delete local/remote branch (unrecoverable; prompts for confirmation)
  --cleanup-merged       Post-merge: pull main, remove worktree, delete local+remote branch
  --cleanup-all-merged   Batch sweep: run --cleanup-merged on every worktree whose PR is MERGED
  --status, --list       Compact table: issue, branch, agent, port, spec state, PR state.
  --status --verbose     Full table: adds PATH, DEV-PID, AGENT-PID, SESSION.
  -h, --help             Show this help and exit

Available adapters:
  claude      Claude Code CLI (default)
  copilot     GitHub Copilot (stub)

For --discard and --cleanup-merged, the issue number is inferred from the branch
when run from inside a linked worktree. See docs/DEVELOPMENT.md for full behavior,
the numbering rule, and the permission model.

Batch example:
  for i in 210 211 212; do scripts/agent.sh --headless "$i"; done
  for i in 210 211 212; do scripts/agent.sh --approve-spec "$i"; done
  scripts/agent.sh --cleanup-all-merged
  scripts/agent.sh --status
EOF
}

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" || $# -eq 0 ]]; then
  print_usage
  exit 0
fi

# REPO_ROOT is the primary worktree (main repo clone), regardless of whether
# the script is invoked from there or from a linked worktree. `git worktree list`
# emits the primary first by contract; fall back to show-toplevel for non-worktree
# checkouts or older git versions.
REPO_ROOT="$(git worktree list --porcelain 2>/dev/null | awk '/^worktree/ {print $2; exit}')"
if [[ -z "${REPO_ROOT:-}" ]]; then
  REPO_ROOT="$(git rev-parse --show-toplevel)"
fi
PARENT_DIR="$(dirname "$REPO_ROOT")"
BASE_PORT=3010
MAX_PORT=3100

# Read a single key from a .agent key=value file.
read_agent_key() {
  local file="$1" key="$2"
  grep "^${key}=" "$file" 2>/dev/null | head -1 | cut -d= -f2- || true
}

# Source an adapter by name. Exits with a clean error listing valid names if not found.
source_adapter() {
  local name="$1"
  local adapter_path="${SCRIPT_DIR}/agents/${name}.sh"
  if [[ ! -f "$adapter_path" ]]; then
    local available
    available="$(cd "${SCRIPT_DIR}/agents" && ls ./*.sh 2>/dev/null | sed 's|^\./||; s|\.sh$||' | tr '\n' ' ' | sed 's/ $//')"
    echo "Unknown agent: ${name}. Available: ${available:-none}" >&2
    exit 1
  fi
  # shellcheck source=/dev/null
  source "$adapter_path"
}

# Populates globals describing the caller's worktree context:
#   CTX_IS_IN_LINKED_WT  0|1 — are we inside a linked (non-primary) worktree?
#   CTX_MAIN_REPO        absolute path to the primary worktree (== REPO_ROOT)
#   CTX_CURRENT_WT       absolute path to the caller's current worktree toplevel
#   CTX_INFERRED_ISSUE   digit string from ^[0-9]+- branch prefix, or empty
# Writes nothing to stdout on success; a helper, not UI.
resolve_worktree_context() {
  CTX_IS_IN_LINKED_WT=0
  CTX_MAIN_REPO=""
  CTX_CURRENT_WT=""
  CTX_INFERRED_ISSUE=""
  local git_common_dir git_dir branch
  git_common_dir="$(git rev-parse --git-common-dir 2>/dev/null || true)"
  git_dir="$(git rev-parse --git-dir 2>/dev/null || true)"
  [[ -z "$git_common_dir" || -z "$git_dir" ]] && return 0
  # Canonicalize — --git-dir can return a relative path
  [[ "$git_common_dir" = /* ]] || git_common_dir="$(cd "$git_common_dir" 2>/dev/null && pwd)"
  [[ "$git_dir" = /* ]]        || git_dir="$(cd "$git_dir" 2>/dev/null && pwd)"
  if [[ "$git_common_dir" != "$git_dir" ]]; then
    CTX_IS_IN_LINKED_WT=1
  fi
  CTX_MAIN_REPO="$REPO_ROOT"
  CTX_CURRENT_WT="$(git rev-parse --show-toplevel 2>/dev/null || true)"
  branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
  if [[ "$branch" =~ ^([0-9]+)- ]]; then
    CTX_INFERRED_ISSUE="${BASH_REMATCH[1]}"
  fi
}

# Emit the stranded-shell warning as the final line of a successful cleanup,
# iff the worktree just removed was the caller's CWD (or a subdir of it).
print_stranded_shell_notice_if_needed() {
  local removed_wt="$1"
  local caller_cwd="$2"
  local main_repo="$3"
  [[ -z "$removed_wt" ]] && return 0  # recovery path may have no dir to compare against
  if [[ "$caller_cwd" == "$removed_wt" || "$caller_cwd" == "$removed_wt"/* ]]; then
    echo "note: your shell's previous CWD ($removed_wt) no longer exists — run \`cd $main_repo\` to continue"
  fi
}

# Poll for a PID to exit, up to timeout_s seconds. Returns 0 when gone, 1 on timeout.
# Used before `git worktree remove` to let `next dev` drop its .next/ file descriptors
# — otherwise macOS raises ENOTEMPTY and the remove fails mid-sequence, leaving an
# orphan directory even though git has already unregistered the worktree admin dir.
wait_for_pid_exit() {
  local pid="$1"
  local timeout_s="${2:-5}"
  local i=0
  local max=$(( timeout_s * 10 ))
  while (( i < max )); do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.1
    ((i++))
  done
  return 1
}

remove_worktree() {
  local issue="$1"
  local caller_cwd
  caller_cwd="$(pwd -P)"  # physical path so we match git's canonical worktree paths (macOS /tmp -> /private/tmp)
  local wt branch push_err
  wt="$(git -C "$REPO_ROOT" worktree list --porcelain \
    | awk -v i="-${issue}-" '/^worktree/ && $2 ~ i {print $2; exit}')"
  if [[ -n "${wt:-}" ]]; then
    branch="$(git -C "$wt" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
  else
    branch="$(git -C "$REPO_ROOT" for-each-ref --format='%(refname:short)' "refs/heads/${issue}-*" | head -1)"
  fi

  if [[ -z "${wt:-}" && -z "${branch:-}" ]]; then
    echo "Nothing to remove: no worktree or branch found for issue $issue."
    exit 0
  fi

  [[ -z "${wt:-}" ]] && echo "note: no registered worktree found for issue $issue — will still clean up branches."

  echo "WARNING: This will permanently discard all uncommitted and unpushed work for issue #${issue}." >&2
  [[ -n "${wt:-}" ]]     && echo "         Worktree:      $wt" >&2
  [[ -z "${wt:-}" ]]     && echo "         Worktree:      (none registered)" >&2
  [[ -n "${branch:-}" ]] && echo "         Branch:        $branch (local + remote will be deleted)" >&2
  [[ -z "${branch:-}" ]] && echo "         Branch:        (none found)" >&2
  echo "         This action is NOT recoverable." >&2
  printf 'Type YES to confirm: '
  local confirm
  read -r confirm
  if [[ "$(tr '[:upper:]' '[:lower:]' <<<"$confirm")" != "yes" ]]; then
    echo "Aborted." >&2
    exit 1
  fi

  if [[ -n "${wt:-}" ]]; then
    if [[ -f "$wt/.agent" ]]; then
      local dev_pid agent_pid
      dev_pid="$(read_agent_key "$wt/.agent" dev-pid)"
      agent_pid="$(read_agent_key "$wt/.agent" agent-pid)"
      [[ -n "${dev_pid:-}" ]] && kill "$dev_pid" 2>/dev/null || true
      [[ -n "${agent_pid:-}" ]] && kill "$agent_pid" 2>/dev/null || true
    fi
    git -C "$REPO_ROOT" worktree remove --force "$wt"
    echo "Removed $wt"
  fi

  if [[ -n "${branch:-}" && "$branch" != "HEAD" ]]; then
    if git -C "$REPO_ROOT" show-ref --verify --quiet "refs/heads/$branch"; then
      git -C "$REPO_ROOT" branch -D "$branch"
    else
      echo "Local branch $branch already removed"
    fi

    if push_err="$(git -C "$REPO_ROOT" push origin --delete "$branch" 2>&1)"; then
      echo "Deleted remote branch origin/$branch"
    elif grep -q "remote ref does not exist" <<<"$push_err"; then
      echo "Remote branch $branch already removed"
    else
      echo "WARNING: could not delete remote branch $branch" >&2
      echo "$push_err" >&2
      echo "Delete the remote manually with:" >&2
      echo "  git push origin --delete $branch" >&2
    fi
  fi

  print_stranded_shell_notice_if_needed "$wt" "$caller_cwd" "$REPO_ROOT"
}

cleanup_merged() {
  local issue="$1"
  local caller_cwd
  caller_cwd="$(pwd -P)"  # physical path so we match git's canonical worktree paths (macOS /tmp -> /private/tmp)
  local wt branch current_branch pr_state push_err dev_pid
  local wt_registered=1
  wt="$(git -C "$REPO_ROOT" worktree list --porcelain \
    | awk -v i="-${issue}-" '/^worktree/ && $2 ~ i {print $2; exit}')"
  if [[ -z "${wt:-}" ]]; then
    # Partial-failure recovery: worktree is no longer registered, but the
    # physical dir, local branch, and/or remote branch may still be around
    # from a prior run that failed mid-sequence. Locate the branch by issue
    # prefix and derive the expected dir path from it.
    branch="$(git -C "$REPO_ROOT" for-each-ref --format='%(refname:short)' "refs/heads/${issue}-*" | head -1)"
    if [[ -z "${branch:-}" ]]; then
      echo "No worktree or local branch found for issue $issue" >&2
      exit 1
    fi
    wt_registered=0
    local candidate="$PARENT_DIR/repo-pulse-$branch"
    if [[ -d "$candidate" ]]; then
      echo "Detected orphaned worktree dir at $candidate (not registered with git); recovering."
      wt="$candidate"
    else
      wt=""
    fi
  else
    branch="$(git -C "$wt" rev-parse --abbrev-ref HEAD)"
    if [[ -z "$branch" || "$branch" == "HEAD" ]]; then
      echo "Could not determine branch for $wt" >&2
      exit 1
    fi
  fi

  # If the primary worktree isn't on main, attempt a clean checkout. Refuse on
  # dirty state — never force-discard the maintainer's in-flight work.
  current_branch="$(git -C "$REPO_ROOT" rev-parse --abbrev-ref HEAD)"
  if [[ "$current_branch" != "main" ]]; then
    echo "Primary worktree at $REPO_ROOT is on '$current_branch'; checking out main..."
    if ! git -C "$REPO_ROOT" checkout main; then
      echo "Cannot check out main in $REPO_ROOT — primary worktree has uncommitted changes or a conflict." >&2
      echo "Resolve it manually (commit/stash/revert) and re-run." >&2
      exit 1
    fi
  fi

  # Verify merge via GitHub PR state, not local ancestry — squash/rebase merges
  # produce a merge commit that is not an ancestor of the local feature branch.
  if ! pr_state="$(cd "$REPO_ROOT" && gh pr view "$branch" --json state -q .state 2>/dev/null)"; then
    echo "Could not determine PR state for $branch." >&2
    echo "Is gh installed and authenticated? If this branch has no PR, use:" >&2
    echo "  scripts/agent.sh --discard $issue" >&2
    exit 1
  fi
  if [[ "$pr_state" != "MERGED" ]]; then
    echo "PR for $branch is $pr_state, not MERGED." >&2
    echo "Use: scripts/agent.sh --discard $issue" >&2
    exit 1
  fi

  echo "Pulling main in $REPO_ROOT ..."
  git -C "$REPO_ROOT" pull --ff-only origin main

  if [[ -n "$wt" && -d "$wt" ]]; then
    dev_pid=""
    local agent_pid=""
    if [[ -f "$wt/.agent" ]]; then
      dev_pid="$(read_agent_key "$wt/.agent" dev-pid)"
      agent_pid="$(read_agent_key "$wt/.agent" agent-pid)"
    fi
    [[ -n "${dev_pid:-}" ]] && kill "$dev_pid" 2>/dev/null || true
    [[ -n "${agent_pid:-}" ]] && kill "$agent_pid" 2>/dev/null || true
    # kill(2) is asynchronous — give next dev a beat to release .next/ handles
    # before git tries to rmdir them, so we don't hit ENOTEMPTY mid-sequence.
    if [[ -n "$dev_pid" ]]; then
      wait_for_pid_exit "$dev_pid" 5 || true
    fi
  fi

  if (( wt_registered )); then
    if git -C "$REPO_ROOT" worktree remove --force "$wt" 2>/dev/null; then
      echo "Removed $wt"
    else
      # If git has already unregistered the worktree but failed to rmdir the
      # physical path (classic ENOTEMPTY with a lingering dev-server fd), the
      # admin side is done — finish the job with a plain rm -rf so the caller
      # doesn't have to chase the orphan manually.
      if git -C "$REPO_ROOT" worktree list --porcelain \
          | awk '/^worktree/ {print $2}' | grep -qxF "$wt"; then
        echo "git worktree remove failed and the worktree is still registered; aborting." >&2
        exit 1
      fi
      echo "git worktree remove left an orphan dir at $wt; removing it now."
      rm -rf "$wt"
      echo "Removed $wt"
    fi
  elif [[ -n "$wt" && -d "$wt" ]]; then
    rm -rf "$wt"
    echo "Removed $wt"
  fi

  if git -C "$REPO_ROOT" show-ref --verify --quiet "refs/heads/$branch"; then
    git -C "$REPO_ROOT" branch -D "$branch"
  else
    echo "Local branch $branch already removed"
  fi

  # Treat "remote ref does not exist" as success — GitHub's "Automatically
  # delete head branches" setting removes the remote at merge time in most
  # repos, so an explicit delete becomes a documented no-op. Any other failure
  # (network, auth, protected branch) warns and exits non-zero; local state
  # is already clean, so only a manual remote delete remains.
  if push_err="$(git -C "$REPO_ROOT" push origin --delete "$branch" 2>&1)"; then
    echo "Deleted remote branch origin/$branch"
  elif grep -q "remote ref does not exist" <<<"$push_err"; then
    echo "Remote branch $branch already removed"
  else
    echo "WARNING: could not delete remote branch $branch" >&2
    echo "$push_err" >&2
    echo "Worktree and local branch were removed; delete the remote manually with:" >&2
    echo "  git push origin --delete $branch" >&2
    print_stranded_shell_notice_if_needed "$wt" "$caller_cwd" "$REPO_ROOT"
    exit 1
  fi

  print_stranded_shell_notice_if_needed "$wt" "$caller_cwd" "$REPO_ROOT"
}

cleanup_all_merged() {
  # Guard: primary worktree must be on main before we start the sweep.
  local current_branch
  current_branch="$(git -C "$REPO_ROOT" rev-parse --abbrev-ref HEAD)"
  if [[ "$current_branch" != "main" ]]; then
    echo "Primary worktree at $REPO_ROOT is on '$current_branch'; checking out main..."
    if ! git -C "$REPO_ROOT" checkout main; then
      echo "Cannot check out main in $REPO_ROOT — primary worktree has uncommitted changes or a conflict." >&2
      echo "Resolve it manually (commit/stash/revert) and re-run." >&2
      exit 1
    fi
  fi

  local cleaned=0 skipped=0 failed=0

  # Collect all linked worktree paths (porcelain emits primary first; skip it).
  local skip_first=1
  while IFS= read -r wt_path; do
    if (( skip_first )); then
      skip_first=0
      continue
    fi

    local branch=""
    branch="$(git -C "$wt_path" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
    if [[ -z "$branch" || "$branch" == "HEAD" ]]; then
      echo "Skipping $wt_path: detached HEAD or cannot determine branch"
      skipped=$(( skipped + 1 ))
      continue
    fi

    local pr_state=""
    if ! pr_state="$(cd "$REPO_ROOT" && gh pr view "$branch" --json state -q .state 2>/dev/null)"; then
      echo "Skipping $branch: no PR found"
      skipped=$(( skipped + 1 ))
      continue
    fi
    if [[ "$pr_state" != "MERGED" ]]; then
      echo "Skipping $branch: PR is $pr_state"
      skipped=$(( skipped + 1 ))
      continue
    fi

    local issue=""
    if [[ "$branch" =~ ^([0-9]+)- ]]; then
      issue="${BASH_REMATCH[1]}"
    else
      echo "Skipping $branch: no numeric issue prefix in branch name"
      skipped=$(( skipped + 1 ))
      continue
    fi

    echo "--- Cleaning issue $issue ($branch) ---"
    if ( cleanup_merged "$issue" ); then
      cleaned=$(( cleaned + 1 ))
    else
      echo "FAILED to clean issue $issue ($branch)" >&2
      failed=$(( failed + 1 ))
    fi
  done < <(git -C "$REPO_ROOT" worktree list --porcelain | awk '/^worktree/ {print $2}')

  echo ""
  echo "$cleaned merged worktrees cleaned, $skipped skipped"
  if (( failed > 0 )); then
    echo "$failed cleanup(s) failed" >&2
    return 1
  fi
}

# Compute SpecKit lifecycle state for a worktree+issue from filesystem artifacts.
# done: spec + plan + tasks all generated
# in-progress: spec + plan generated, awaiting tasks/implementation
# paused: spec generated, awaiting human approval before plan
# no-spec: no spec for this issue (e.g. --no-speckit worktree)
compute_spec_state() {
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

# Print a single-screen status table of every linked worktree provisioned by this script.
# Terse (default): ISSUE  BRANCH  AGENT  PORT  SPEC  PR
# Verbose (--verbose): ISSUE  BRANCH  AGENT  PATH  PORT  DEV-PID  AGENT-PID  SPEC  PR  SESSION
# Spec states:  no-spec | paused | in-progress | done
# PR states:    none | OPEN | MERGED | CLOSED
print_status() {
  local verbose="${1:-0}"
  local branch issue agent_name port dev_pid_str agent_pid_str spec_state pr_state session_str
  local _p _dpid _apid _prst _sid _agt skip_first wt_path
  local -a rows

  if (( verbose )); then
    rows=("ISSUE\tBRANCH\tAGENT\tPATH\tPORT\tDEV-PID\tAGENT-PID\tSPEC\tPR\tSESSION")
  else
    rows=("ISSUE\tBRANCH\tAGENT\tPORT\tSPEC\tPR")
  fi

  skip_first=1
  while IFS= read -r wt_path; do
    if (( skip_first )); then
      skip_first=0
      continue
    fi

    branch="$(git -C "$wt_path" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"

    issue=""
    if [[ "$branch" =~ ^([0-9]+)- ]]; then
      issue="${BASH_REMATCH[1]}"
    fi

    agent_name="-"
    if [[ -f "$wt_path/.agent" ]]; then
      _agt="$(read_agent_key "$wt_path/.agent" agent)"
      [[ -n "${_agt:-}" ]] && agent_name="$_agt"
    fi

    port="-"
    if [[ -f "$wt_path/.env.local" ]]; then
      _p="$(grep '^PORT=' "$wt_path/.env.local" 2>/dev/null | head -1 | cut -d= -f2 || true)"
      [[ -n "${_p:-}" ]] && port="$_p"
    fi

    dev_pid_str="-"
    if [[ -f "$wt_path/.agent" ]]; then
      _dpid="$(read_agent_key "$wt_path/.agent" dev-pid)"
      if [[ -n "${_dpid:-}" ]]; then
        if kill -0 "$_dpid" 2>/dev/null; then
          dev_pid_str="$_dpid"
        else
          dev_pid_str="${_dpid}(dead)"
        fi
      fi
    fi

    agent_pid_str="-"
    if [[ -f "$wt_path/.agent" ]]; then
      _apid="$(read_agent_key "$wt_path/.agent" agent-pid)"
      if [[ -n "${_apid:-}" ]]; then
        if kill -0 "$_apid" 2>/dev/null; then
          agent_pid_str="$_apid"
        else
          agent_pid_str="${_apid}(dead)"
        fi
      fi
    fi

    # Spec state derived from filesystem artifacts scoped to this issue's
    # spec directory to avoid matching specs from prior features on main.
    spec_state="$(compute_spec_state "$wt_path" "$issue")"

    pr_state="none"
    if [[ -n "${branch:-}" && "$branch" != "HEAD" ]]; then
      if _prst="$(cd "$REPO_ROOT" && gh pr view "$branch" --json state -q .state 2>/dev/null)"; then
        [[ -n "${_prst:-}" ]] && pr_state="$_prst"
      fi
    fi

    session_str="-"
    if [[ -f "$wt_path/.agent" ]]; then
      _sid="$(read_agent_key "$wt_path/.agent" session-id)"
      [[ -n "${_sid:-}" ]] && session_str="${_sid:0:8}"
    fi

    if (( verbose )); then
      rows+=("${issue:-?}\t${branch:-?}\t${agent_name}\t${wt_path}\t${port}\t${dev_pid_str}\t${agent_pid_str}\t${spec_state}\t${pr_state}\t${session_str}")
    else
      rows+=("${issue:-?}\t${branch:-?}\t${agent_name}\t${port}\t${spec_state}\t${pr_state}")
    fi
  done < <(git -C "$REPO_ROOT" worktree list --porcelain | awk '/^worktree/ {print $2}')

  printf '%b\n' "${rows[@]}" | column -t -s $'\t'
}

release_paused_session() {
  local issue="$1"
  local prompt="$2"
  local wt agent
  wt="$(git -C "$REPO_ROOT" worktree list --porcelain \
    | awk -v i="-${issue}-" '/^worktree/ && $2 ~ i {print $2; exit}')"
  if [[ -z "${wt:-}" ]]; then
    echo "No worktree found for issue $issue" >&2
    exit 1
  fi
  if [[ ! -f "$wt/.agent" ]]; then
    echo "No .agent file found for issue $issue; cannot resume non-interactively." >&2
    echo "Use 'cd $wt && claude --resume' instead." >&2
    exit 1
  fi
  agent="$(read_agent_key "$wt/.agent" agent)"
  if [[ -z "${agent:-}" ]]; then
    echo ".agent file for issue $issue missing 'agent' key; cannot resume." >&2
    exit 1
  fi
  source_adapter "$agent"
  # Paused state is reached once /speckit.specify has written a spec file.
  if ! compgen -G "$wt/specs/*/spec.md" > /dev/null; then
    echo "Spec not yet generated for issue $issue; paused state not reached." >&2
    echo "Tail $wt/agent.log to confirm and retry once the pause is reported." >&2
    exit 1
  fi
  agent_resume "$wt" "$prompt"
  echo "Released pause for issue $issue; Stage 2 running in background."
  echo "Tail: $wt/agent.log"
}

# Populates RESOLVED_CLEANUP_ISSUE from an explicit CLI arg or, failing that,
# from branch-name inference when the caller is inside a linked worktree.
# Exits non-zero (no destructive action) when no arg is given and the caller
# is either outside a linked worktree, or on a branch without a numeric prefix.
# Uses a global rather than stdout because `exit` inside $() only exits the
# subshell, which would let the caller proceed with an empty issue value.
resolve_cleanup_issue() {
  local flag="$1"      # "--cleanup-merged" or "--discard"
  local explicit="$2"  # the positional arg from the CLI, possibly empty
  RESOLVED_CLEANUP_ISSUE=""
  if [[ -n "$explicit" ]]; then
    RESOLVED_CLEANUP_ISSUE="$explicit"
    return 0
  fi
  resolve_worktree_context
  if (( CTX_IS_IN_LINKED_WT == 0 )); then
    echo "Usage: $0 $flag <issue>" >&2
    exit 1
  fi
  if [[ -z "$CTX_INFERRED_ISSUE" ]]; then
    local current_branch
    current_branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo '<unknown>')"
    echo "Cannot infer issue number from branch '$current_branch' (expected prefix matching ^[0-9]+-)." >&2
    echo "Re-run with an explicit issue number:" >&2
    echo "  $0 $flag <issue>" >&2
    exit 1
  fi
  RESOLVED_CLEANUP_ISSUE="$CTX_INFERRED_ISSUE"
}

if [[ "${1:-}" == "--discard" ]]; then
  resolve_cleanup_issue --discard "${2:-}"
  remove_worktree "$RESOLVED_CLEANUP_ISSUE"
  exit 0
fi

if [[ "${1:-}" == "--cleanup-merged" ]]; then
  resolve_cleanup_issue --cleanup-merged "${2:-}"
  cleanup_merged "$RESOLVED_CLEANUP_ISSUE"
  exit 0
fi

if [[ "${1:-}" == "--cleanup-all-merged" ]]; then
  if ! cleanup_all_merged; then
    exit 1
  fi
  exit 0
fi

if [[ "${1:-}" == "--status" || "${1:-}" == "--list" ]]; then
  _verbose=0
  [[ "${2:-}" == "--verbose" ]] && _verbose=1
  print_status "$_verbose"
  exit 0
fi

if [[ "${1:-}" == "--approve-spec" ]]; then
  [[ -n "${2:-}" ]] || { echo "Usage: $0 --approve-spec <issue>" >&2; exit 1; }
  release_paused_session "$2" "proceed"
  exit 0
fi

if [[ "${1:-}" == "--revise-spec" ]]; then
  [[ -n "${2:-}" ]] || { echo "Usage: $0 --revise-spec <issue> <feedback>" >&2; exit 1; }
  if [[ -z "${3:-}" ]]; then
    echo "--revise-spec requires non-empty feedback" >&2
    exit 1
  fi
  release_paused_session "$2" "$3"
  exit 0
fi

# --- Spawn flow ---

HEADLESS=0
NO_SPECKIT=0
AGENT="claude"
POSITIONAL=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --headless)   HEADLESS=1; shift ;;
    --no-speckit) NO_SPECKIT=1; shift ;;
    --agent)
      [[ -n "${2:-}" ]] || { echo "--agent requires a name" >&2; exit 1; }
      AGENT="$2"; shift 2 ;;
    --*) echo "Unknown option: $1" >&2; exit 1 ;;
    *)   POSITIONAL+=("$1"); shift ;;
  esac
done

if [[ ${#POSITIONAL[@]} -eq 0 ]]; then
  echo "Usage: $0 [--agent <name>] [--headless] [--no-speckit] <issue-number> [slug]" >&2
  exit 1
fi
ISSUE="${POSITIONAL[0]}"
SLUG="${POSITIONAL[1]:-}"

source_adapter "$AGENT"

if (( NO_SPECKIT )); then
  echo "WARNING: --no-speckit skips the SpecKit lifecycle and the spec-review pause." >&2
  echo "         This run is fully automated with NO human-in-the-loop checkpoint." >&2
  echo "         The agent will make changes and open a PR without spec approval." >&2
fi

if [[ -z "$SLUG" ]]; then
  if ! command -v gh >/dev/null 2>&1; then
    echo "gh CLI not found; pass a slug explicitly" >&2
    exit 1
  fi
  title="$(gh issue view "$ISSUE" --json title -q .title 2>/dev/null || true)"
  if [[ -z "$title" ]]; then
    echo "Could not fetch title for issue #$ISSUE; pass a slug explicitly" >&2
    exit 1
  fi
  # lowercase, non-alnum -> '-', collapse/trim dashes, cap length
  SLUG="$(printf '%s' "$title" \
    | tr '[:upper:]' '[:lower:]' \
    | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//' \
    | cut -c1-40 \
    | sed -E 's/-+$//')"
  SLUG="${SLUG:-work}"
  echo "Derived slug from issue title: $SLUG"
fi

BRANCH="${ISSUE}-${SLUG}"
WT_PATH="${PARENT_DIR}/repo-pulse-${ISSUE}-${SLUG}"

# 1. Find a free port
port=$BASE_PORT
while (( port <= MAX_PORT )); do
  if ! lsof -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    break
  fi
  ((port++))
done
if (( port > MAX_PORT )); then
  echo "No free port in ${BASE_PORT}-${MAX_PORT}" >&2
  exit 1
fi

# 2. Create worktree
if [[ -d "$WT_PATH" ]]; then
  echo "Worktree already exists: $WT_PATH" >&2
  exit 1
fi
git -C "$REPO_ROOT" worktree add "$WT_PATH" -b "$BRANCH"

# 3. Port + install
# Seed .env.local from the main repo's .env.local (OAuth creds, etc.), then set PORT.
if [[ -f "$REPO_ROOT/.env.local" ]]; then
  grep -v '^PORT=' "$REPO_ROOT/.env.local" > "$WT_PATH/.env.local"
  echo "Copied .env.local from $REPO_ROOT"
else
  : > "$WT_PATH/.env.local"
  echo "WARNING: $REPO_ROOT/.env.local not found — worktree will start without OAuth creds" >&2
fi
echo "PORT=$port" >> "$WT_PATH/.env.local"
( cd "$WT_PATH" && npm install --silent )

# 4. Start dev server; capture PID for the .agent state file
DEV_PID="$( cd "$WT_PATH" && nohup npm run dev -- -p "$port" > dev.log 2>&1 & echo $! )"
echo "Dev server: http://localhost:$port (log: $WT_PATH/dev.log)"

# 5. Generate session UUID and write the .agent state file
# Core writes: agent, session-id, dev-pid. The adapter appends agent-pid.
if ! command -v uuidgen >/dev/null 2>&1; then
  echo "uuidgen not found; required for session addressability" >&2
  exit 1
fi
SESSION_ID="$(uuidgen | tr '[:upper:]' '[:lower:]')"
{
  echo "agent=$AGENT"
  echo "session-id=$SESSION_ID"
  echo "dev-pid=$DEV_PID"
} > "$WT_PATH/.agent"

# 6. Build kickoff prompt and launch agent
if (( NO_SPECKIT )); then
  KICKOFF="Work on GitHub issue #${ISSUE}. Read CLAUDE.md for project conventions. Skip the SpecKit lifecycle — do NOT run /speckit.specify, /speckit.plan, /speckit.tasks, or /speckit.implement. Make the changes directly, then push the branch and open a PR; do not merge.

Dev server is already running on port ${port}."
else
  KICKOFF="Work on GitHub issue #${ISSUE}. Follow CLAUDE.md (read constitution, DEVELOPMENT.md, PRODUCT.md). Run the SpecKit lifecycle in two stages with a mandatory human-in-the-loop pause in between:

STAGE 1: Run /speckit.specify. When it completes, report the generated spec file path and STOP. Do NOT proceed to /speckit.plan. Wait for explicit user approval — one of the phrases \"proceed\", \"approved\", or \"go to plan\". If the user replies with spec revisions instead of an approval phrase, update the spec and re-enter the paused state (report the updated spec path and wait again). Only an explicit approval phrase releases the pause.

STAGE 2: After approval, run /speckit.plan, then /speckit.tasks, then /speckit.implement in sequence. When done, push the branch and open a PR; do not merge.

Dev server is already running on port ${port}."
fi

agent_launch "$WT_PATH" "$ISSUE" "$port" "$SESSION_ID" "$KICKOFF" "$HEADLESS"
