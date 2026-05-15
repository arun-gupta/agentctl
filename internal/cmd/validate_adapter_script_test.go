package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func requireBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available on this system")
	}
	if runtime.GOOS == "windows" {
		t.Skip("validate-adapter.sh requires bash; skipping on Windows")
	}
}

func TestValidateAdapterScriptUsage(t *testing.T) {
	requireBash(t)

	script := validateAdapterScriptPath(t)
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("script is not executable: mode=%#o", info.Mode().Perm())
	}

	cmd := exec.Command("bash", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err == nil {
		t.Fatal("expected missing-adapter invocation to fail")
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1", exitErr.ExitCode())
	}

	output := stdout.String() + stderr.String()
	if !strings.Contains(output, "Usage: validate-adapter.sh <adapter-name> [--issue N] [--repo-path PATH]") {
		t.Fatalf("usage output missing, got:\n%s", output)
	}
}

func TestValidateAdapterScriptDefaultIssueAndOverrides(t *testing.T) {
	requireBash(t)

	script := validateAdapterScriptPath(t)
	stubBin := t.TempDir()
	repoPath := initValidateAdapterRepo(t)
	makeValidateAdapterStubs(t, stubBin)

	cmd := exec.Command("bash", script, "gemini", "--repo-path", repoPath)
	cmd.Env = append(os.Environ(),
		"PATH="+stubBin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}

	output := string(out)
	if !strings.Contains(output, "[validate-adapter] adapter: gemini  repo: arun-gupta/agentctl-test  issue: #77") {
		t.Fatalf("summary line missing discovered issue, got:\n%s", output)
	}
	if !strings.Contains(output, "[PASS] install hint shown when binary is missing") {
		t.Fatalf("install-hint check did not pass, got:\n%s", output)
	}
	if !strings.Contains(output, "Result: 7 automated checks passed, 0 failed, 3 manual checks pending") {
		t.Fatalf("result summary mismatch, got:\n%s", output)
	}

	ghLog, err := os.ReadFile(filepath.Join(stubBin, "gh.log"))
	if err != nil {
		t.Fatalf("read gh log: %v", err)
	}
	if !strings.Contains(string(ghLog), "issue list --repo arun-gupta/agentctl-test --limit 1 --json number -q .[0].number") {
		t.Fatalf("gh issue list call missing, got:\n%s", ghLog)
	}
	if strings.Contains(string(ghLog), "issue create") || strings.Contains(string(ghLog), "issue delete") {
		t.Fatalf("script must not create/delete issues, got:\n%s", ghLog)
	}

	cmd = exec.Command("bash", script, "cursor", "--issue", "42", "--repo-path", repoPath)
	cmd.Env = append(os.Environ(),
		"PATH="+stubBin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script with explicit issue failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "issue: #77") || !strings.Contains(string(out), "issue: #42") {
		t.Fatalf("explicit issue override not respected, got:\n%s", out)
	}
}

func TestValidateAdapterScriptCleanupOnFailure(t *testing.T) {
	requireBash(t)

	script := validateAdapterScriptPath(t)
	stubBin := t.TempDir()
	repoPath := initValidateAdapterRepo(t)
	makeValidateAdapterStubs(t, stubBin)

	cmd := exec.Command("bash", script, "opencode", "--repo-path", repoPath)
	cmd.Env = append(os.Environ(),
		"PATH="+stubBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AGENTCTL_FAKE_NO_CHANGES=1",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected script to fail when no worktree changes are present\n%s", out)
	}

	output := string(out)
	if !strings.Contains(output, "[FAIL] no file changes detected in worktree") {
		t.Fatalf("expected no-file-changes failure, got:\n%s", output)
	}

	worktreeList := runValidateAdapterCommand(t, repoPath, "git", "-C", repoPath, "worktree", "list", "--porcelain")
	if strings.Contains(worktreeList, "/77-opencode") {
		t.Fatalf("worktree cleanup failed, remaining worktrees:\n%s", worktreeList)
	}

	branches := runValidateAdapterCommand(t, repoPath, "git", "-C", repoPath, "branch", "--list")
	if strings.Contains(branches, "77-opencode") {
		t.Fatalf("branch cleanup failed, remaining branches:\n%s", branches)
	}
}

func validateAdapterScriptPath(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	return filepath.Join(root, "scripts", "validate-adapter.sh")
}

func initValidateAdapterRepo(t *testing.T) string {
	t.Helper()

	repoPath := filepath.Join(t.TempDir(), "agentctl-test")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	runValidateAdapterCommand(t, repoPath, "git", "init", "-b", "main", repoPath)
	runValidateAdapterCommand(t, repoPath, "git", "-C", repoPath, "config", "user.name", "Test User")
	runValidateAdapterCommand(t, repoPath, "git", "-C", repoPath, "config", "user.email", "test@example.com")
	runValidateAdapterCommand(t, repoPath, "git", "-C", repoPath, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runValidateAdapterCommand(t, repoPath, "git", "-C", repoPath, "add", "README.md")
	runValidateAdapterCommand(t, repoPath, "git", "-C", repoPath, "commit", "-m", "seed")

	return repoPath
}

func makeValidateAdapterStubs(t *testing.T, dir string) {
	t.Helper()

	writeValidateAdapterStub(t, filepath.Join(dir, "gh"), `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>`+`"`+filepath.Join(dir, "gh.log")+`"`+`
if [[ "${1:-}" == "issue" && "${2:-}" == "list" ]]; then
  echo 77
  exit 0
fi
echo "unexpected gh invocation: $*" >&2
exit 1
`)

	// agentctl stub mirrors real output formats:
	//   info     — "Coding Agents:" block with ✗/✓ markers and install hints
	//   status   — tab-separated table with ISSUE as first column (NR>1)
	//   start    — creates a real git worktree so resolve_worktree and cleanup work
	//   logs     — emits a log line
	//   attach   — exits 0 immediately
	writeValidateAdapterStub(t, filepath.Join(dir, "agentctl"), `#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "info" ]]; then
  cat <<'EOF'
Coding Agents:
  ✗ gemini       (not installed) — install with: npm install -g @google/gemini-cli
  ✗ cursor       (not installed) — install with: brew install --cask cursor-cli
  ✗ opencode     (not installed) — install with: npm install -g opencode@latest
EOF
  exit 0
fi

if [[ "${1:-}" == "agent" && "${2:-}" == "start" ]]; then
  adapter=""
  issue=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --agent)   adapter="$2"; shift 2 ;;
      --headless) shift ;;
      agent|start) shift ;;
      *) issue="$1"; shift ;;
    esac
  done

  repo_path="$(pwd)"
  worktree_path="${repo_path}/${issue}-${adapter}"
  branch_name="${issue}-${adapter}"
  git -C "$repo_path" worktree add -b "$branch_name" "$worktree_path" HEAD >/dev/null

  if [[ "${AGENTCTL_FAKE_NO_CHANGES:-0}" != "1" ]]; then
    printf 'adapter=%s issue=%s\n' "$adapter" "$issue" >"$worktree_path/result.txt"
    git -C "$worktree_path" add result.txt >/dev/null
    git -C "$worktree_path" commit -m "agent output" >/dev/null
  fi
  exit 0
fi

if [[ "${1:-}" == "agent" && "${2:-}" == "status" ]]; then
  printf 'ISSUE\tBRANCH\tAGENT\tPORT\tSPEC\tPR\n'
  printf '77\t77-gemini\tgemini\t3010\tno-spec\tnone\n'
  printf '42\t42-cursor\tcursor\t3011\tno-spec\tnone\n'
  exit 0
fi

if [[ "${1:-}" == "agent" && "${2:-}" == "logs" ]]; then
  echo "log line"
  exit 0
fi

if [[ "${1:-}" == "agent" && "${2:-}" == "attach" ]]; then
  exit 0
fi

echo "unexpected agentctl invocation: $*" >&2
exit 1
`)
}

func writeValidateAdapterStub(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", path, err)
	}
}

func runValidateAdapterCommand(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}
