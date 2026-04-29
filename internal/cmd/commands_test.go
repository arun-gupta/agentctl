package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arun-gupta/agentctl/internal/config"
	"github.com/arun-gupta/agentctl/internal/notify"
	"github.com/arun-gupta/agentctl/internal/sdd"
	"github.com/arun-gupta/agentctl/internal/state"
)

// TestMain handles the hidden __stream-log subprocess that launchAgent/agentResume
// spawn in headless claude mode. Without this, the subprocess would restart the
// test suite instead of running the converter.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__stream-log" {
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: __stream-log <wtDir>")
			os.Exit(1)
		}
		if err := runStreamLog(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestTitleToSlug(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{"Go rewrite: implement agentctl core CLI", "go-rewrite-implement-agentctl-core-cli"},
		{"Fix bug #42 in parser!", "fix-bug-42-in-parser"},
		{"  Leading spaces  ", "leading-spaces"},
		{"multiple   spaces   between", "multiple-spaces-between"},
		{"ALL CAPS TITLE", "all-caps-title"},
		{"a-b-c", "a-b-c"},
		{"", ""},
		// 40-char cap: input is trimmed to 40 chars then trailing dashes stripped
		{"aaaaaaaaaa-bbbbbbbbbb-cccccccccc-ddddddddd-eeee", "aaaaaaaaaa-bbbbbbbbbb-cccccccccc-ddddddd"},
	}
	for _, tt := range tests {
		got := titleToSlug(tt.title)
		if got != tt.want {
			t.Errorf("titleToSlug(%q) = %q, want %q", tt.title, got, tt.want)
		}
	}
}

func TestComputeSpecState_noSpec(t *testing.T) {
	dir := t.TempDir()
	state := computeSpecState(dir, "42", "plain", true)
	if state != "no-spec" {
		t.Errorf("expected no-spec, got %q", state)
	}
}

func TestComputeSpecState_emptyIssue(t *testing.T) {
	dir := t.TempDir()
	state := computeSpecState(dir, "", "plain", true)
	if state != "no-spec" {
		t.Errorf("expected no-spec for empty issue, got %q", state)
	}
}

func TestComputeSpecState_paused(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "42-my-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "spec.md"), []byte("spec"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := computeSpecState(dir, "42", "speckit", true)
	if state != "paused" {
		t.Errorf("expected paused, got %q", state)
	}
}

func TestComputeSpecState_inProgress(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "42-my-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"spec.md", "plan.md"} {
		if err := os.WriteFile(filepath.Join(specDir, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	state := computeSpecState(dir, "42", "speckit", true)
	if state != "in-progress" {
		t.Errorf("expected in-progress, got %q", state)
	}
}

func TestComputeSpecState_done(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "42-my-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"spec.md", "plan.md", "tasks.md"} {
		if err := os.WriteFile(filepath.Join(specDir, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	state := computeSpecState(dir, "42", "speckit", true)
	if state != "done" {
		t.Errorf("expected done, got %q", state)
	}
}

func TestComputeSpecState_speckitStyleAbsent(t *testing.T) {
	dir := t.TempDir()
	if got := computeSpecState(dir, "42", "speckit", true); got != "no-spec" {
		t.Errorf("computeSpecState empty dir = %q, want %q", got, "no-spec")
	}
}

func TestComputeSpecState_speckitStylePresent(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "42-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "spec.md"), []byte("spec"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := computeSpecState(dir, "42", "speckit", true); got != "paused" {
		t.Errorf("computeSpecState speckit spec = %q, want %q", got, "paused")
	}
}

func TestComputeSpecState_plainStyle(t *testing.T) {
	// plain SDD writes specs/spec.md directly; status should show "paused".
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "specs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "specs", "spec.md"), []byte("spec"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := computeSpecState(dir, "15", "plain", true)
	if got != "paused" {
		t.Errorf("computeSpecState with plain-style spec = %q, want %q", got, "paused")
	}
}

func TestComputeSpecState_plainSpecExistsNoSDD(t *testing.T) {
	// specs/spec.md exists in the repo (e.g. committed from a prior run) but no
	// SDD was requested for this issue — status must show "no-spec", not "paused".
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "specs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "specs", "spec.md"), []byte("spec"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := computeSpecState(dir, "11", "", true)
	if got != "no-spec" {
		t.Errorf("computeSpecState no SDD + specs/spec.md = %q, want %q", got, "no-spec")
	}
}

func TestComputeSpecState_legacyWorktree(t *testing.T) {
	// Legacy worktrees written before the sdd= key was introduced have sddSet=false.
	// computeSpecState must fall back to filesystem heuristics so existing worktrees
	// with spec artifacts continue to show their real lifecycle state.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "specs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "specs", "spec.md"), []byte("spec"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := computeSpecState(dir, "11", "", false)
	if got != "paused" {
		t.Errorf("computeSpecState legacy worktree + specs/spec.md = %q, want %q", got, "paused")
	}
}

// ─── OpenHands streaming ──────────────────────────────────────────────────────

func TestExtractOpenHandsBlock_agentMessage(t *testing.T) {
	block := `{
  "kind": "MessageEvent",
  "source": "agent",
  "llm_message": {
    "content": [{"type": "text", "text": "Hello! How can I help?"}]
  }
}`
	got := extractOpenHandsBlock(block)
	if got != "Hello! How can I help?" {
		t.Errorf("extractOpenHandsBlock agent message = %q, want %q", got, "Hello! How can I help?")
	}
}

func TestExtractOpenHandsBlock_userMessageSkipped(t *testing.T) {
	block := `{
  "kind": "MessageEvent",
  "source": "user",
  "llm_message": {
    "content": [{"type": "text", "text": "say hello"}]
  }
}`
	if got := extractOpenHandsBlock(block); got != "" {
		t.Errorf("extractOpenHandsBlock user message should be empty, got %q", got)
	}
}

func TestExtractOpenHandsBlock_actionEvent(t *testing.T) {
	block := `{
  "kind": "ActionEvent",
  "source": "agent",
  "tool_name": "bash",
  "summary": "Run the test suite"
}`
	got := extractOpenHandsBlock(block)
	if got != "[bash] Run the test suite" {
		t.Errorf("extractOpenHandsBlock action = %q, want %q", got, "[bash] Run the test suite")
	}
}

func TestExtractOpenHandsBlock_unknownKindSkipped(t *testing.T) {
	block := `{"kind": "SystemPromptEvent", "source": "agent"}`
	if got := extractOpenHandsBlock(block); got != "" {
		t.Errorf("extractOpenHandsBlock unknown kind should be empty, got %q", got)
	}
}

func TestConvertOpenHandsStream_extractsAgentMessage(t *testing.T) {
	input := `Initializing agent...
--JSON Event--
{
  "kind": "MessageEvent",
  "source": "user",
  "llm_message": {"content": [{"type": "text", "text": "say hello"}]}
}
Agent is working
--JSON Event--
{
  "kind": "MessageEvent",
  "source": "agent",
  "llm_message": {"content": [{"type": "text", "text": "Hello! How can I assist?"}]}
}
Agent finished
`
	var out strings.Builder
	convertOpenHandsStream(strings.NewReader(input), &out)
	result := out.String()
	if !strings.Contains(result, "Hello! How can I assist?") {
		t.Errorf("convertOpenHandsStream missing agent message; got:\n%s", result)
	}
	if strings.Contains(result, "say hello") {
		t.Errorf("convertOpenHandsStream should skip user message; got:\n%s", result)
	}
	if !strings.Contains(result, "Initializing agent...") {
		t.Errorf("convertOpenHandsStream should pass through non-event lines; got:\n%s", result)
	}
	if !strings.Contains(result, "Agent is working") {
		t.Errorf("convertOpenHandsStream should pass through 'Agent is working'; got:\n%s", result)
	}
}

func TestConvertOpenHandsStream_suppressesNoise(t *testing.T) {
	input := `Rich detected a non-interactive or unsupported terminal; interactive UI may not render correctly
To override Rich's detection, you can set TTY_INTERACTIVE=1
Initializing agent...
`
	var out strings.Builder
	convertOpenHandsStream(strings.NewReader(input), &out)
	result := out.String()
	if strings.Contains(result, "Rich detected") {
		t.Errorf("convertOpenHandsStream should suppress Rich noise; got:\n%s", result)
	}
	if !strings.Contains(result, "Initializing agent...") {
		t.Errorf("convertOpenHandsStream should pass through init line; got:\n%s", result)
	}
}

func TestBuildKickoff_substitution(t *testing.T) {
	kickoff := buildKickoff("42", "3010")
	if strings.Contains(kickoff, "{issue}") {
		t.Error("buildKickoff did not substitute {issue}")
	}
	if strings.Contains(kickoff, "{port}") {
		t.Error("buildKickoff did not substitute {port}")
	}
	if !strings.Contains(kickoff, "42") {
		t.Error("buildKickoff missing issue number 42")
	}
	if !strings.Contains(kickoff, "3010") {
		t.Error("buildKickoff missing port 3010")
	}
}

func TestBuildKickoff_noPort_omitsDevServerLine(t *testing.T) {
	kickoff := buildKickoff("42", "")
	if strings.Contains(kickoff, "port") || strings.Contains(kickoff, "{port}") {
		t.Errorf("buildKickoff with empty port must not mention port, got:\n%s", kickoff)
	}
	if !strings.Contains(kickoff, "open a PR") {
		t.Errorf("buildKickoff with empty port must still instruct to open a PR, got:\n%s", kickoff)
	}
}

func TestBuildKickoff_isAgentNeutral(t *testing.T) {
	kickoff := buildKickoff("42", "3010")
	if strings.Contains(kickoff, "CLAUDE.md") {
		t.Errorf("buildKickoff must not reference CLAUDE.md; got:\n%s", kickoff)
	}
	if !strings.Contains(kickoff, "AGENTS.md") && !strings.Contains(kickoff, "README.md") {
		t.Errorf("buildKickoff must mention an agent-neutral convention file; got:\n%s", kickoff)
	}
	if !strings.Contains(kickoff, "Do not run agentctl") {
		t.Errorf("buildKickoff must tell agents not to run agent-launcher CLIs; got:\n%s", kickoff)
	}
	if contains(kickoff, "/speckit") {
		t.Error("buildKickoff must not contain speckit-specific commands")
	}
}

func TestStartCmd_noSDDFlagRemoved(t *testing.T) {
	c := NewStartCmd()
	if f := c.Flags().Lookup("no-sdd"); f != nil {
		t.Error("--no-sdd flag must not be registered; it was removed")
	}
}

func TestStartCmd_sddFlagExists(t *testing.T) {
	c := NewStartCmd()
	f := c.Flags().Lookup("sdd")
	if f == nil {
		t.Fatal("--sdd flag must be registered")
	}
	if f.DefValue != "" {
		t.Errorf("--sdd default should be '' (empty), got %q", f.DefValue)
	}
}

func TestKickoffPrompt_speckit(t *testing.T) {
	m, err := sdd.Get("speckit")
	if err != nil {
		t.Fatal(err)
	}
	kickoff := m.KickoffPrompt("42", "3010")
	if !contains(kickoff, "/speckit.specify") {
		t.Error("speckit kickoff should contain /speckit.specify")
	}
	if !contains(kickoff, "3010") {
		t.Error("kickoff should contain the port number")
	}
}

func TestDash(t *testing.T) {
	if dash("") != "-" {
		t.Error("dash(\"\") should return \"-\"")
	}
	if dash("claude") != "claude" {
		t.Error("dash(\"claude\") should return \"claude\"")
	}
}

func TestPidStatus_empty(t *testing.T) {
	if got := pidStatus(""); got != "-" {
		t.Errorf("pidStatus(\"\") = %q, want \"-\"", got)
	}
}

func TestPidStatus_alive(t *testing.T) {
	pid := strconv.Itoa(os.Getpid())
	if got := pidStatus(pid); got != pid {
		t.Errorf("pidStatus(self) = %q, want %q", got, pid)
	}
}

func TestPidStatus_dead(t *testing.T) {
	// PID 9999999 is almost certainly not running.
	got := pidStatus("9999999")
	want := "9999999(dead)"
	if got != want {
		t.Errorf("pidStatus(9999999) = %q, want %q", got, want)
	}
}

func TestResolveIssueArg_withArg(t *testing.T) {
	issue, err := resolveIssueArg("test", []string{"42"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue != "42" {
		t.Errorf("got %q, want %q", issue, "42")
	}
}

func TestResolveIssueArg_noArgs_notLinked(t *testing.T) {
	// Running from the primary worktree (not a linked one) must return an error.
	_, err := resolveIssueArg("test", []string{})
	if err == nil {
		t.Error("expected error when no arg given and not inside a linked worktree")
	}
}

// ─── parseIssueURL ───────────────────────────────────────────────────────────

func TestParseIssueURL_bareNumber(t *testing.T) {
	owner, repo, issueNum, ok := parseIssueURL("42")
	if ok {
		t.Errorf("parseIssueURL(\"42\") should return ok=false")
	}
	if owner != "" || repo != "" {
		t.Errorf("expected empty owner/repo for bare number, got %q %q", owner, repo)
	}
	if issueNum != "42" {
		t.Errorf("expected issueNum=42, got %q", issueNum)
	}
}

func TestParseIssueURL_validURL(t *testing.T) {
	owner, repo, issueNum, ok := parseIssueURL("https://github.com/myorg/myrepo/issues/99")
	if !ok {
		t.Fatal("parseIssueURL should return ok=true for a valid URL")
	}
	if owner != "myorg" {
		t.Errorf("owner = %q, want %q", owner, "myorg")
	}
	if repo != "myrepo" {
		t.Errorf("repo = %q, want %q", repo, "myrepo")
	}
	if issueNum != "99" {
		t.Errorf("issueNum = %q, want %q", issueNum, "99")
	}
}

func TestParseIssueURL_trailingSlash(t *testing.T) {
	_, _, issueNum, ok := parseIssueURL("https://github.com/myorg/myrepo/issues/7/")
	if !ok {
		t.Fatal("trailing slash should be accepted")
	}
	if issueNum != "7" {
		t.Errorf("issueNum = %q, want %q", issueNum, "7")
	}
}

func TestParseIssueURL_invalidPaths(t *testing.T) {
	cases := []string{
		"https://github.com/myorg/myrepo/pull/42",    // pull request URL
		"https://github.com/myorg/myrepo/issues/",    // missing number
		"https://github.com/myorg/myrepo/issues/abc", // non-numeric
		"https://github.com/myorg/myrepo",            // no issues path
		"https://example.com/owner/repo/issues/1",    // wrong host
	}
	for _, c := range cases {
		_, _, _, ok := parseIssueURL(c)
		if ok {
			t.Errorf("parseIssueURL(%q) should return ok=false", c)
		}
	}
}

// ─── matchesGitHubOrigin ─────────────────────────────────────────────────────

// initGitRepoWithOrigin creates a bare git repo and sets a given origin URL.
// Returns the repo directory path.
func initGitRepoWithOrigin(t *testing.T, originURL string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"remote", "add", "origin", originURL},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestMatchesGitHubOrigin_https(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://github.com/myorg/myrepo.git")
	if !matchesGitHubOrigin(dir, "myorg", "myrepo") {
		t.Error("expected matchesGitHubOrigin to return true for https URL")
	}
}

func TestMatchesGitHubOrigin_ssh(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "git@github.com:myorg/myrepo.git")
	if !matchesGitHubOrigin(dir, "myorg", "myrepo") {
		t.Error("expected matchesGitHubOrigin to return true for SSH URL")
	}
}

func TestMatchesGitHubOrigin_noGitSuffix(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://github.com/myorg/myrepo")
	if !matchesGitHubOrigin(dir, "myorg", "myrepo") {
		t.Error("expected matchesGitHubOrigin to return true when .git suffix absent")
	}
}

func TestMatchesGitHubOrigin_wrongOwner(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://github.com/otherorg/myrepo.git")
	if matchesGitHubOrigin(dir, "myorg", "myrepo") {
		t.Error("expected matchesGitHubOrigin to return false for wrong owner")
	}
}

func TestMatchesGitHubOrigin_noOrigin(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if matchesGitHubOrigin(dir, "myorg", "myrepo") {
		t.Error("expected matchesGitHubOrigin to return false when no origin remote")
	}
}

// ─── locateOrCloneRepo ───────────────────────────────────────────────────────

func TestLocateOrCloneRepo_cwdMatch(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://github.com/myorg/myrepo.git")
	chdirTemp(t, dir)

	got, err := locateOrCloneRepo("myorg", "myrepo", io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Resolve symlinks: on macOS t.TempDir() is under /var which symlinks to /private/var.
	wantResolved, _ := filepath.EvalSymlinks(dir)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != wantResolved {
		t.Errorf("got %q, want %q", got, dir)
	}
}

func TestLocateOrCloneRepo_siblingMatch(t *testing.T) {
	// Create a parent directory to hold both CWD and sibling repos.
	parent := t.TempDir()
	cwdDir := filepath.Join(parent, "some-other-repo")
	if err := os.MkdirAll(cwdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// CWD repo does not match target.
	cwdRepo := initGitRepoWithOrigin(t, "https://github.com/otherorg/otherrepo.git")
	chdirTemp(t, cwdRepo)

	// Sibling directory at ../myrepo relative to CWD.
	cwd, _ := os.Getwd()
	siblingDir := filepath.Join(filepath.Dir(cwd), "myrepo")
	if err := os.MkdirAll(siblingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(siblingDir) })
	// Init a git repo with matching origin inside the sibling.
	for _, args := range [][]string{
		{"init"},
		{"remote", "add", "origin", "https://github.com/myorg/myrepo.git"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = siblingDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	got, err := locateOrCloneRepo("myorg", "myrepo", io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != siblingDir {
		t.Errorf("got %q, want %q", got, siblingDir)
	}
}

func TestLocateOrCloneRepo_siblingWrongOrigin(t *testing.T) {
	// CWD repo does not match.
	cwdRepo := initGitRepoWithOrigin(t, "https://github.com/otherorg/otherrepo.git")
	chdirTemp(t, cwdRepo)

	// Create a sibling named "myrepo" with the wrong origin.
	cwd, _ := os.Getwd()
	siblingDir := filepath.Join(filepath.Dir(cwd), "myrepo")
	if err := os.MkdirAll(siblingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(siblingDir) })
	for _, args := range [][]string{
		{"init"},
		{"remote", "add", "origin", "https://github.com/wrongorg/myrepo.git"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = siblingDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	_, err := locateOrCloneRepo("myorg", "myrepo", io.Discard)
	if err == nil {
		t.Fatal("expected error when sibling has wrong origin")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("expected 'does not match' in error, got: %v", err)
	}
}

// ─── repoRootForIssue ────────────────────────────────────────────────────────

func TestRepoRootForIssue_bareNumber(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://github.com/myorg/myrepo.git")
	chdirTemp(t, dir)

	root, issueNum, ghArg, err := repoRootForIssue("42", io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Resolve symlinks: on macOS t.TempDir() is under /var which symlinks to /private/var.
	wantResolved, _ := filepath.EvalSymlinks(dir)
	gotResolved, _ := filepath.EvalSymlinks(root)
	if gotResolved != wantResolved {
		t.Errorf("root = %q, want %q", root, dir)
	}
	if issueNum != "42" {
		t.Errorf("issueNum = %q, want %q", issueNum, "42")
	}
	if ghArg != "42" {
		t.Errorf("ghArg = %q, want %q", ghArg, "42")
	}
}

func TestRepoRootForIssue_urlCwdMatch(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://github.com/myorg/myrepo.git")
	chdirTemp(t, dir)

	const rawURL = "https://github.com/myorg/myrepo/issues/99"
	root, issueNum, ghArg, err := repoRootForIssue(rawURL, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Resolve symlinks: on macOS t.TempDir() is under /var which symlinks to /private/var.
	wantResolved, _ := filepath.EvalSymlinks(dir)
	gotResolved, _ := filepath.EvalSymlinks(root)
	if gotResolved != wantResolved {
		t.Errorf("root = %q, want %q", root, dir)
	}
	if issueNum != "99" {
		t.Errorf("issueNum = %q, want %q", issueNum, "99")
	}
	if ghArg != rawURL {
		t.Errorf("ghArg = %q, want %q", ghArg, rawURL)
	}
}

func TestRemoveBranchRefs_alreadyRemovedIsQuiet(t *testing.T) {
	repo := initGitRepoWithBareOrigin(t)

	createCommittedFile(t, repo, "tracked.txt", "initial\n")
	gitRun(t, repo, "checkout", "-b", "main")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "initial")
	gitRun(t, repo, "push", "-u", "origin", "main")

	const branch = "113-fix-discard-output"
	gitRun(t, repo, "checkout", "-b", branch)
	gitRun(t, repo, "push", "-u", "origin", branch)
	gitRun(t, repo, "checkout", "main")
	gitRun(t, repo, "branch", "-D", branch)
	gitRun(t, repo, "push", "origin", "--delete", branch)

	if err := removeBranchRefs(repo, branch); err != nil {
		t.Fatalf("removeBranchRefs: want nil when branches already gone, got %v", err)
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

// ─── validateAdapter ─────────────────────────────────────────────────────────

func TestValidateAdapter_known(t *testing.T) {
	if err := validateAdapter("claude"); err != nil {
		t.Errorf("validateAdapter(\"claude\") = %v; want nil", err)
	}
}

func TestValidateAdapter_unknown(t *testing.T) {
	if err := validateAdapter("nonexistent-xyz-abc"); err == nil {
		t.Error("validateAdapter(nonexistent) expected error, got nil")
	}
}

// ─── validateSDD ─────────────────────────────────────────────────────────────

func TestValidateSDD_nonSpeckit_noCheck(t *testing.T) {
	// Non-speckit methodologies require no filesystem check.
	if err := validateSDD("plain", t.TempDir()); err != nil {
		t.Errorf("validateSDD(plain) = %v; want nil", err)
	}
	if err := validateSDD("", t.TempDir()); err != nil {
		t.Errorf("validateSDD(\"\") = %v; want nil", err)
	}
}

func TestValidateSDD_speckit_missingSkills(t *testing.T) {
	dir := t.TempDir() // no .claude/commands/ directory
	err := validateSDD("speckit", dir)
	if err == nil {
		t.Fatal("validateSDD(speckit) expected error when skills missing, got nil")
	}
	if !strings.Contains(err.Error(), "SpecKit skills not found") {
		t.Errorf("error should mention 'SpecKit skills not found', got: %v", err)
	}
	if !strings.Contains(err.Error(), ".claude/commands") {
		t.Errorf("error should mention .claude/commands install path, got: %v", err)
	}
	if !strings.Contains(err.Error(), "speckit.specify.md") {
		t.Errorf("error should mention required speckit skill files, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--sdd=plain") {
		t.Errorf("error should suggest --sdd=plain alternative, got: %v", err)
	}
}

func TestValidateSDD_speckit_skillsPresent(t *testing.T) {
	dir := t.TempDir()
	cmdDir := filepath.Join(dir, ".claude", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "speckit.specify.md"), []byte("# speckit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateSDD("speckit", dir); err != nil {
		t.Errorf("validateSDD(speckit) with skills present = %v; want nil", err)
	}
}

// ─── waitForFile ─────────────────────────────────────────────────────────────

func TestWaitForFile_exists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testfile")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := waitForFile(path, time.Second); err != nil {
		t.Errorf("waitForFile on existing file: %v", err)
	}
}

func TestWaitForFile_timeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing")
	err := waitForFile(path, 50*time.Millisecond)
	if err == nil {
		t.Error("waitForFile expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "did not appear") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ─── findFreePort ────────────────────────────────────────────────────────────

func TestFindFreePort(t *testing.T) {
	port, err := findFreePort(3010, 3100)
	if err != nil {
		t.Fatalf("findFreePort: %v", err)
	}
	if port < 3010 || port > 3100 {
		t.Errorf("port %d out of range [3010, 3100]", port)
	}
}

// ─── generateUUID ────────────────────────────────────────────────────────────

func TestGenerateUUID(t *testing.T) {
	uuid, err := generateUUID()
	if err != nil {
		t.Fatalf("generateUUID: %v", err)
	}
	if len(uuid) < 32 {
		t.Errorf("UUID too short: %q (want ≥32 chars)", uuid)
	}
	if uuid != strings.ToLower(uuid) {
		t.Errorf("UUID not lowercase: %q", uuid)
	}
}

// ─── launchAgent ─────────────────────────────────────────────────────────────

// chdirTemp changes the working directory to dir for the duration of the test
// and restores it in t.Cleanup.
func chdirTemp(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func initGitRepoWithBareOrigin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	repo := filepath.Join(root, "repo")

	cmd := exec.Command("git", "init", "--bare", origin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	cmd = exec.Command("git", "clone", origin, repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}

	gitRun(t, repo, "config", "user.email", "test@example.com")
	gitRun(t, repo, "config", "user.name", "Test")
	return repo
}

func createCommittedFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeLocalAdapter writes content to .agentctl/adapters/<name>.yml under dir.
func writeLocalAdapter(t *testing.T, dir, name, content string) {
	t.Helper()
	adapterDir := filepath.Join(dir, ".agentctl", "adapters")
	if err := os.MkdirAll(adapterDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adapterDir, name+".yml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLaunchAgent_unknownAdapter(t *testing.T) {
	dir := t.TempDir()
	err := launchAgent("nonexistent-xyz-abc", dir, "42", "3010", "sess-123", "kickoff", "", true, false, false, &bytes.Buffer{})
	if err == nil {
		t.Error("expected error for unknown adapter")
	}
}

func TestLaunchAgent_binaryNotFound(t *testing.T) {
	dir := t.TempDir()
	writeLocalAdapter(t, dir, "fakebinary", "binary: __nonexistent_binary_xyz__\n")
	chdirTemp(t, dir)

	err := launchAgent("fakebinary", dir, "42", "3010", "sess-123", "kickoff", "", true, false, false, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error when binary not found")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("expected 'not found on PATH' in error, got: %v", err)
	}
}

func TestLaunchAgent_headless(t *testing.T) {
	dir := t.TempDir()
	// Use `echo` as the agent binary — always on PATH, exits immediately.
	writeLocalAdapter(t, dir, "echoagent",
		"binary: echo\nsession: --session\n")
	chdirTemp(t, dir)

	var out bytes.Buffer
	err := launchAgent("echoagent", dir, "42", "3010", "sess-abc", "do the thing", "", true, false, false, &out)
	if err != nil {
		t.Fatalf("launchAgent headless: %v", err)
	}

	// Verify agent-pid was recorded in .agent.
	af, err := state.Read(dir)
	if err != nil {
		t.Fatalf("state.Read: %v", err)
	}
	if af.AgentPID == "" {
		t.Error("expected agent-pid to be written to .agent after headless launch")
	}
	if _, err := strconv.Atoi(af.AgentPID); err != nil {
		t.Errorf("agent-pid %q is not a valid integer: %v", af.AgentPID, err)
	}

	// Without --sdd: operational hints, no resume/speckit language.
	outStr := out.String()
	if !strings.Contains(outStr, "Agent PID") {
		t.Errorf("missing 'Agent PID' in headless output:\n%s", outStr)
	}
	for _, unwanted := range []string{"Session ID", "agentctl resume", "sends approval", "rewrites the spec"} {
		if strings.Contains(outStr, unwanted) {
			t.Errorf("non-SDD headless output must not contain %q:\n%s", unwanted, outStr)
		}
	}
	for _, want := range []string{"agentctl logs 42", "agentctl attach 42", "agentctl discard 42"} {
		if !strings.Contains(outStr, want) {
			t.Errorf("missing %q in non-SDD headless output:\n%s", want, outStr)
		}
	}
}

func TestLaunchAgent_headless_withSDD_showsResumeHint(t *testing.T) {
	dir := t.TempDir()
	writeLocalAdapter(t, dir, "echoagent",
		"binary: echo\nsession: --session\n")
	chdirTemp(t, dir)

	var out bytes.Buffer
	err := launchAgent("echoagent", dir, "42", "3010", "sess-abc", "do the thing", "plain", true, false, false, &out)
	if err != nil {
		t.Fatalf("launchAgent headless SDD: %v", err)
	}

	outStr := out.String()
	// SDD headless: must show logs/attach/discard so user can follow progress,
	// plus the resume hint for the spec-review checkpoint.
	for _, want := range []string{
		"agentctl logs 42",
		"agentctl attach 42",
		"agentctl discard 42",
		"agentctl resume 42",
	} {
		if !strings.Contains(outStr, want) {
			t.Errorf("SDD headless output missing %q:\n%s", want, outStr)
		}
	}
}

// TestLaunchAgent_headless_notify verifies that a desktop notification is
// fired after the headless agent exits when notify=true.
func TestLaunchAgent_headless_notify(t *testing.T) {
	dir := t.TempDir()
	writeLocalAdapter(t, dir, "echoagent",
		"binary: echo\nsession: --session\n")
	chdirTemp(t, dir)

	// Replace the notification sender so we can capture the call without
	// requiring OS notification tools.
	origFn := notify.SendFn
	t.Cleanup(func() { notify.SendFn = origFn })

	notified := make(chan [2]string, 1)
	notify.SendFn = func(title, message string) {
		select {
		case notified <- [2]string{title, message}:
		default:
		}
	}

	var out bytes.Buffer
	err := launchAgent("echoagent", dir, "42", "3010", "sess-abc", "do the thing", "", true, false, true, &out)
	if err != nil {
		t.Fatalf("launchAgent headless notify: %v", err)
	}

	// Wait for the background notification goroutine to fire.
	select {
	case got := <-notified:
		if got[0] != "agentctl" {
			t.Errorf("notification title = %q, want %q", got[0], "agentctl")
		}
		if !strings.Contains(got[1], "42") {
			t.Errorf("notification message %q does not mention issue 42", got[1])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for desktop notification")
	}
}

func TestLaunchAgent_nonHeadless_exitsWhenAgentDone(t *testing.T) {
	dir := t.TempDir()
	// Use `echo` as the agent binary — always on PATH, exits immediately.
	writeLocalAdapter(t, dir, "echoagent",
		"binary: echo\nsession: --session\n")
	chdirTemp(t, dir)

	// Run launchAgent in non-headless mode in a goroutine; it must return
	// automatically once the agent process (echo) exits, without requiring
	// Ctrl+C or any other intervention.
	done := make(chan error, 1)
	go func() {
		done <- launchAgent("echoagent", dir, "42", "3010", "sess-abc", "do the thing", "", false, false, false, &bytes.Buffer{})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("launchAgent non-headless: %v", err)
		}
	case <-time.After(5 * time.Second):
		// launchAgent listens on sigCh for os.Interrupt/SIGTERM to unblock its
		// select loop. Sending SIGINT to this process delivers it to sigCh via
		// signal.Notify, which causes launchAgent to return — letting the goroutine
		// above exit cleanly rather than leaking into subsequent tests or hanging
		// the full `go test` run until the global timeout.
		if p, err := os.FindProcess(os.Getpid()); err == nil {
			_ = p.Signal(os.Interrupt)
		}

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("launchAgent did not return after agent process exited and cleanup returned error: %v", err)
			}
			t.Fatal("launchAgent did not return after agent process exited — required interrupt-driven cleanup before failing")
		case <-time.After(2 * time.Second):
			t.Fatal("launchAgent did not return after agent process exited, and did not exit after interrupt-driven cleanup")
		}
	}
}

func TestLaunchAgent_nonHeadless_exitPrintsCleanupHint(t *testing.T) {
	dir := t.TempDir()
	writeLocalAdapter(t, dir, "echoagent",
		"binary: echo\nsession: --session\n")
	chdirTemp(t, dir)

	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- launchAgent("echoagent", dir, "42", "3010", "sess-abc", "do the thing", "", false, false, false, &out)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("launchAgent non-headless: %v", err)
		}
	case <-time.After(5 * time.Second):
		if p, err := os.FindProcess(os.Getpid()); err == nil {
			_ = p.Signal(os.Interrupt)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("launchAgent did not return after agent process exited: %v", err)
			}
			t.Fatal("launchAgent did not return after agent process exited")
		case <-time.After(2 * time.Second):
			t.Fatal("launchAgent hung even after interrupt")
		}
	}

	outStr := out.String()
	// Non-SDD run: expect cleanup hint, not resume hint.
	want := "agentctl cleanup 42"
	if !strings.Contains(outStr, want) {
		t.Errorf("missing cleanup hint %q in foreground-exit output:\n%s", want, outStr)
	}
	for _, unwanted := range []string{"agentctl logs", "agentctl attach", "agentctl discard", "agentctl resume"} {
		if strings.Contains(outStr, unwanted) {
			t.Errorf("foreground-exit output must not contain %q:\n%s", unwanted, outStr)
		}
	}
}

// TestLaunchAgent_nonClaudeNonHeadless_outputStreamed verifies that plain-text
// output from non-claude adapters is written to agent.log in non-headless mode.
func TestLaunchAgent_nonClaudeNonHeadless_outputStreamed(t *testing.T) {
	dir := t.TempDir()
	// Use echo with an explicit launch command that emits a known plain-text
	// line — simulates codex/copilot output (no JSON events).
	writeLocalAdapter(t, dir, "echoagent2",
		"binary: echo\nlaunch: echo hello from codex\n")
	chdirTemp(t, dir)

	done := make(chan error, 1)
	go func() {
		done <- launchAgent("echoagent2", dir, "42", "3010", "sess-abc", "do the thing", "", false, false, false, &bytes.Buffer{})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("launchAgent: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("launchAgent did not return")
	}

	logPath := filepath.Join(dir, "agent.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read agent.log: %v", err)
	}
	if !strings.Contains(string(content), "hello from codex") {
		t.Errorf("agent.log missing plain-text output; got:\n%s", content)
	}
}

// TestLaunchAgent_claudeNonHeadlessInjectsStreamJsonAndVerbose verifies that
// launchAgent appends --output-format, stream-json, and --verbose to the
// command line when adapterName is "claude" and headless is false.
func TestLaunchAgent_claudeNonHeadlessInjectsStreamJsonAndVerbose(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "argv.txt")

	// Create a stub script that records its argv and exits cleanly.
	scriptPath := filepath.Join(dir, "claude-stub")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + argsFile + "\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	// Shadow the built-in "claude" adapter with our stub binary.
	writeLocalAdapter(t, dir, "claude", "binary: "+scriptPath+"\nsession: --session\n")
	chdirTemp(t, dir)

	done := make(chan error, 1)
	go func() {
		done <- launchAgent("claude", dir, "42", "3010", "sess-abc", "kickoff text", "", false, false, false, &bytes.Buffer{})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("launchAgent: %v", err)
		}
	case <-time.After(5 * time.Second):
		if p, err := os.FindProcess(os.Getpid()); err == nil {
			_ = p.Signal(os.Interrupt)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("launchAgent did not return after interrupt: %v", err)
			}
			t.Fatal("launchAgent timed out — did not detect agent exit")
		case <-time.After(2 * time.Second):
			t.Fatal("launchAgent hung even after interrupt")
		}
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading argv file: %v", err)
	}
	argsStr := string(argsData)
	for _, want := range []string{"--output-format", "stream-json", "--verbose"} {
		if !strings.Contains(argsStr, want) {
			t.Errorf("missing %q in spawned claude argv: %q", want, argsStr)
		}
	}
}

// TestLaunchAgent_claudeHeadlessUsesStreamJson verifies that launchAgent
// appends --output-format stream-json --verbose to the claude command line in
// headless mode so intermediate tool steps are captured in agent.log.
func TestLaunchAgent_claudeHeadlessUsesStreamJson(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "argv.txt")

	scriptPath := filepath.Join(dir, "claude-stub")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + argsFile + "\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	writeLocalAdapter(t, dir, "claude", "binary: "+scriptPath+"\nsession: --session\n")
	chdirTemp(t, dir)

	if err := launchAgent("claude", dir, "42", "3010", "sess-abc", "kickoff text", "", true, false, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("launchAgent headless: %v", err)
	}

	// In headless mode launchAgent returns before the subprocess exits;
	// poll until argsFile appears.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(argsFile); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading argv file: %v", err)
	}
	argsStr := string(argsData)
	for _, want := range []string{"--output-format", "stream-json", "--verbose"} {
		if !strings.Contains(argsStr, want) {
			t.Errorf("missing %q in headless claude argv: %q", want, argsStr)
		}
	}
}

// TestLaunchAgent_nonHeadless_sigintPrintsHints verifies that pressing Ctrl+C
// while launchAgent is streaming output in non-headless mode prints the
// expected reconnection hints (logs, attach, discard) to the provided writer.
func TestLaunchAgent_nonHeadless_sigintPrintsHints(t *testing.T) {
	dir := t.TempDir()

	// Write a stub agent that sleeps long enough for SIGINT to arrive first.
	scriptPath := filepath.Join(dir, "sleepagent")
	script := "#!/bin/sh\nsleep 30\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLocalAdapter(t, dir, "sleepagent", "binary: "+scriptPath+"\n")
	chdirTemp(t, dir)

	var outBuf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- launchAgent("sleepagent", dir, "42", "3010", "sess-abc", "do the thing", "", false, false, false, &outBuf)
	}()

	// Give launchAgent time to start the agent process and register its signal
	// handler before we send the interrupt.
	time.Sleep(500 * time.Millisecond)

	if p, err := os.FindProcess(os.Getpid()); err == nil {
		_ = p.Signal(os.Interrupt)
	}

	select {
	case launchErr := <-done:
		if launchErr != nil {
			t.Fatalf("launchAgent: %v", launchErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("launchAgent did not return after SIGINT")
	}

	out := outBuf.String()
	for _, want := range []string{
		"agent still running in background",
		"agentctl logs 42",
		"agentctl attach 42",
		"agentctl discard 42",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in out after Ctrl+C, got:\n%s", want, out)
		}
	}
}

// ─── resume command flags ─────────────────────────────────────────────────────

func TestResumeCmd_headlessFlag(t *testing.T) {
	c := NewResumeCmd()
	f := c.Flags().Lookup("headless")
	if f == nil {
		t.Fatal("--headless flag must be registered on resume cmd")
	}
	if f.DefValue != "false" {
		t.Errorf("--headless default should be false, got %q", f.DefValue)
	}
}

func TestResumeCmd_quietFlag(t *testing.T) {
	c := NewResumeCmd()
	f := c.Flags().Lookup("quiet")
	if f == nil {
		t.Fatal("--quiet flag must be registered on resume cmd")
	}
	if f.DefValue != "false" {
		t.Errorf("--quiet default should be false, got %q", f.DefValue)
	}
}

// ─── runReleasePausedSession gating ──────────────────────────────────────────

// setupResumeWorktree creates a git repo with a linked worktree at a path
// containing "-<issue>-" and writes an .agent file into it.
func setupResumeWorktree(t *testing.T, issue string, af state.AgentFile) (repo, wtPath string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	repo = initGitRepoForStale(t)
	wtPath = filepath.Join(t.TempDir(), "repo-"+issue+"-feature")
	addWorktree(t, repo, wtPath, issue+"-feature")
	if err := state.Write(wtPath, af); err != nil {
		t.Fatal(err)
	}
	return repo, wtPath
}

// TestRunReleasePausedSession_nonSDD_noSpec asserts that a plain (non-SDD) run
// is NOT blocked by the missing-spec guard and proceeds past it, failing only
// at adapter validation.
func TestRunReleasePausedSession_nonSDD_noSpec(t *testing.T) {
	repo, _ := setupResumeWorktree(t, "42", state.AgentFile{
		Agent:  "claude",
		SDD:    "",   // non-SDD run
		SDDSet: true, // sdd= key was present (new worktree created without --sdd)
	})
	chdirTemp(t, repo)

	err := runReleasePausedSession("42", "", false, false, false)

	// Must NOT block with "spec not yet generated" — the spec gate does not
	// apply to non-SDD worktrees.
	if err != nil && strings.Contains(err.Error(), "spec not yet generated") {
		t.Errorf("non-SDD run should not be blocked by spec gate; got: %v", err)
	}
}

// TestRunReleasePausedSession_SDD_noSpec asserts that an SDD run IS blocked
// when no spec has been generated yet.
func TestRunReleasePausedSession_SDD_noSpec(t *testing.T) {
	repo, _ := setupResumeWorktree(t, "43", state.AgentFile{
		Agent:  "claude",
		SDD:    "plain", // SDD run with a methodology set
		SDDSet: true,
	})
	chdirTemp(t, repo)

	err := runReleasePausedSession("43", "", false, false, false)

	if err == nil {
		t.Fatal("expected 'spec not yet generated' error for SDD run without spec, got nil")
	}
	if !strings.Contains(err.Error(), "spec not yet generated") {
		t.Errorf("expected 'spec not yet generated' in error; got: %v", err)
	}
}

// TestRunReleasePausedSession_SDD_withSpec asserts that an SDD run is NOT
// blocked once the spec pause checkpoint has been reached (spec file present).
func TestRunReleasePausedSession_SDD_withSpec(t *testing.T) {
	repo, wtPath := setupResumeWorktree(t, "44", state.AgentFile{
		Agent:  "claude",
		SDD:    "plain",
		SDDSet: true,
	})
	// Create the spec file so computeSpecState returns "paused" or "in-progress".
	specDir := filepath.Join(wtPath, "specs", "44-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "spec.md"), []byte("# spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chdirTemp(t, repo)

	err := runReleasePausedSession("44", "", false, false, false)

	// Must NOT block with "spec not yet generated" once spec exists.
	if err != nil && strings.Contains(err.Error(), "spec not yet generated") {
		t.Errorf("SDD run with spec should not be blocked by spec gate; got: %v", err)
	}
}

// ─── agentResume ─────────────────────────────────────────────────────────────

func TestLaunchAgent_nonZeroExitLogsToStderr(t *testing.T) {
	dir := t.TempDir()
	// Use `false` as the agent binary — always exits with code 1.
	writeLocalAdapter(t, dir, "falseagent", "binary: false\n")
	chdirTemp(t, dir)

	// Redirect os.Stderr to a pipe so we can capture the error message.
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = oldStderr })

	// Run launchAgent in non-headless mode. It returns only after exitCh is
	// closed, which happens after fmt.Fprintf(os.Stderr, ...) in the reaper
	// goroutine — so by the time launchAgent returns, the message is already
	// captured in the pipe.
	launchErr := launchAgent("falseagent", dir, "42", "3010", "sess-abc", "do the thing", "", false, false, false, &bytes.Buffer{})

	// Close the write end and restore stderr before reading.
	w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("reading captured stderr: %v", err)
	}
	r.Close()

	// launchAgent itself does not return the agent's exit code as an error.
	if launchErr != nil {
		t.Fatalf("launchAgent: unexpected error: %v", launchErr)
	}
	if !strings.Contains(buf.String(), "agent exited") {
		t.Errorf("expected 'agent exited' on stderr for non-zero exit, got: %q", buf.String())
	}
}

func TestAgentResume_unknownAdapter(t *testing.T) {
	dir := t.TempDir()
	err := agentResume("nonexistent-xyz-abc", dir, "42", "sess-123", "my feedback", true, false, false)
	if err == nil {
		t.Error("expected error for unknown adapter")
	}
}

func TestAgentResume_headless_success(t *testing.T) {
	dir := t.TempDir()
	// Use `echo` as the resume binary — always on PATH, exits immediately.
	writeLocalAdapter(t, dir, "echoagent",
		"binary: echo\nsession: --session\n")
	chdirTemp(t, dir)

	if err := agentResume("echoagent", dir, "42", "sess-123", "my feedback", true, false, false); err != nil {
		t.Fatalf("agentResume headless: %v", err)
	}

	agentState, err := os.ReadFile(filepath.Join(dir, ".agent"))
	if err != nil {
		t.Fatalf("read .agent: %v", err)
	}
	if !strings.Contains(string(agentState), "agent-pid=") {
		t.Fatalf(".agent missing agent-pid entry:\n%s", string(agentState))
	}

	// The background echo process may not have written yet; poll briefly.
	var logData []byte
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		logData, _ = os.ReadFile(filepath.Join(dir, "agent.log"))
		if strings.Contains(string(logData), "sess-123") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(string(logData), "sess-123") {
		t.Fatalf("agent.log missing resumed session output:\n%s", string(logData))
	}
}

// TestAgentResume_headless_notify verifies that a desktop notification is
// fired after the headless resume agent exits when sendNotify=true.
func TestAgentResume_headless_notify(t *testing.T) {
	dir := t.TempDir()
	writeLocalAdapter(t, dir, "echoagent",
		"binary: echo\nsession: --session\n")
	chdirTemp(t, dir)

	origFn := notify.SendFn
	t.Cleanup(func() { notify.SendFn = origFn })

	notified := make(chan [2]string, 1)
	notify.SendFn = func(title, message string) {
		select {
		case notified <- [2]string{title, message}:
		default:
		}
	}

	if err := agentResume("echoagent", dir, "42", "sess-123", "my feedback", true, false, true); err != nil {
		t.Fatalf("agentResume headless notify: %v", err)
	}

	// Wait for the background notification goroutine to fire.
	select {
	case got := <-notified:
		if got[0] != "agentctl" {
			t.Errorf("notification title = %q, want %q", got[0], "agentctl")
		}
		if !strings.Contains(got[1], "42") {
			t.Errorf("notification message %q does not mention issue 42", got[1])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for desktop notification from agentResume")
	}
}

func TestAgentResume_nonHeadless_exitsWhenAgentDone(t *testing.T) {
	dir := t.TempDir()
	// Use `echo` as the resume binary — always on PATH, exits immediately.
	writeLocalAdapter(t, dir, "echoagent",
		"binary: echo\nsession: --session\n")
	chdirTemp(t, dir)

	done := make(chan error, 1)
	go func() {
		done <- agentResume("echoagent", dir, "42", "sess-123", "my feedback", false, false, false)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agentResume non-headless: %v", err)
		}
	case <-time.After(5 * time.Second):
		if p, err := os.FindProcess(os.Getpid()); err == nil {
			_ = p.Signal(os.Interrupt)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("agentResume did not return after agent exit and cleanup returned error: %v", err)
			}
			t.Fatal("agentResume did not return after agent process exited — required interrupt-driven cleanup before failing")
		case <-time.After(2 * time.Second):
			t.Fatal("agentResume hung even after interrupt")
		}
	}
}

func TestAgentResume_nonHeadless_exitPrintsCleanupHint(t *testing.T) {
	dir := t.TempDir()
	writeLocalAdapter(t, dir, "echoagent",
		"binary: echo\nsession: --session\n")
	chdirTemp(t, dir)

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	done := make(chan error, 1)
	go func() {
		done <- agentResume("echoagent", dir, "42", "sess-123", "my feedback", false, false, false)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agentResume non-headless: %v", err)
		}
	case <-time.After(5 * time.Second):
		if p, err := os.FindProcess(os.Getpid()); err == nil {
			_ = p.Signal(os.Interrupt)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("agentResume did not return after agent exit: %v", err)
			}
			t.Fatal("agentResume did not return after agent process exited")
		case <-time.After(2 * time.Second):
			t.Fatal("agentResume hung even after interrupt")
		}
	}

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}
	r.Close()

	out := buf.String()
	// After resume exit, the agent is done — expect cleanup hint, not resume hint.
	want := "agentctl cleanup 42"
	if !strings.Contains(out, want) {
		t.Errorf("missing cleanup hint %q in foreground-exit output:\n%s", want, out)
	}
	for _, unwanted := range []string{"agentctl logs", "agentctl attach", "agentctl discard"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("foreground-exit output must not contain %q:\n%s", unwanted, out)
		}
	}
}

func TestAgentResume_nonHeadless_sigintPrintsHints(t *testing.T) {
	dir := t.TempDir()

	scriptPath := filepath.Join(dir, "sleepagent")
	script := "#!/bin/sh\nsleep 30\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLocalAdapter(t, dir, "sleepagent", "binary: "+scriptPath+"\n")
	chdirTemp(t, dir)

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	done := make(chan error, 1)
	go func() {
		done <- agentResume("sleepagent", dir, "42", "sess-123", "feedback", false, false, false)
	}()

	time.Sleep(500 * time.Millisecond)

	if p, err := os.FindProcess(os.Getpid()); err == nil {
		_ = p.Signal(os.Interrupt)
	}

	select {
	case launchErr := <-done:
		if launchErr != nil {
			t.Fatalf("agentResume: %v", launchErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agentResume did not return after SIGINT")
	}

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}
	r.Close()

	out := buf.String()
	for _, want := range []string{
		"agent still running in background",
		"agentctl logs 42",
		"agentctl attach 42",
		"agentctl discard 42",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in stdout after Ctrl+C, got:\n%s", want, out)
		}
	}
}

// ─── HOME isolation ───────────────────────────────────────────────────────────

// TestLaunchAgent_homeIsolation verifies that a process spawned by launchAgent
// sees HOME set to $wtPath/.agent-home and not the user's real home directory.
func TestLaunchAgent_homeIsolation(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")

	// Stub script: record the HOME env variable and exit.
	scriptPath := filepath.Join(dir, "envagent")
	script := "#!/bin/sh\necho \"HOME=$HOME\" > \"" + envFile + "\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	writeLocalAdapter(t, dir, "envagent", "binary: "+scriptPath+"\nsession: --session\n")
	chdirTemp(t, dir)

	if err := launchAgent("envagent", dir, "42", "3010", "sess-abc", "do the thing", "", true, false, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("launchAgent headless: %v", err)
	}

	// Poll until the stub writes the env file.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(envFile); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("reading env file: %v", err)
	}
	want := "HOME=" + filepath.Join(dir, ".agent-home")
	if !strings.Contains(string(data), want) {
		t.Errorf("expected HOME to be %q; got: %q", want, strings.TrimSpace(string(data)))
	}
}

// TestAgentResume_homeIsolation verifies that a process spawned by agentResume
// sees HOME set to $wtPath/.agent-home and not the user's real home directory.
func TestAgentResume_homeIsolation(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")

	// Stub script: record the HOME env variable and exit.
	scriptPath := filepath.Join(dir, "envagent")
	script := "#!/bin/sh\necho \"HOME=$HOME\" > \"" + envFile + "\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	writeLocalAdapter(t, dir, "envagent", "binary: "+scriptPath+"\nsession: --session\n")
	chdirTemp(t, dir)

	if err := agentResume("envagent", dir, "42", "sess-abc", "feedback", true, false, false); err != nil {
		t.Fatalf("agentResume headless: %v", err)
	}

	// Poll until the stub writes the env file.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(envFile); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("reading env file: %v", err)
	}
	want := "HOME=" + filepath.Join(dir, ".agent-home")
	if !strings.Contains(string(data), want) {
		t.Errorf("expected HOME to be %q; got: %q", want, strings.TrimSpace(string(data)))
	}
}

// TestAgentEnv_rejectsSymlink verifies that agentEnv returns an error when
// .agent-home already exists as a symlink (prevents HOME redirection attacks).
func TestAgentEnv_rejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := t.TempDir()
	agentHome := filepath.Join(dir, ".agent-home")
	if err := os.Symlink(target, agentHome); err != nil {
		t.Skipf("cannot create symlink (requires elevated privileges on Windows): %v", err)
	}

	_, err := agentEnv(dir)
	if err == nil {
		t.Fatal("expected error when .agent-home is a symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink; got: %v", err)
	}
}

// TestAgentEnv_ghConfigSymlink verifies that agentEnv creates a real directory
// at .agent-home/.config and a symlink .agent-home/.config/gh → <HOME>/.config/gh,
// rather than symlinking the entire ~/.config tree.
func TestAgentEnv_ghConfigSymlink(t *testing.T) {
	// Build a fake HOME containing .config/gh
	fakeHome := t.TempDir()
	ghConfigSrc := filepath.Join(fakeHome, ".config", "gh")
	if err := os.MkdirAll(ghConfigSrc, 0o755); err != nil {
		t.Fatalf("setup fake HOME: %v", err)
	}

	// Point HOME at our fake directory so os.UserHomeDir picks it up.
	t.Setenv("HOME", fakeHome)

	dir := t.TempDir()
	if _, err := agentEnv(dir); err != nil {
		t.Fatalf("agentEnv: %v", err)
	}

	agentHome := filepath.Join(dir, ".agent-home")

	// .agent-home/.config must be a real directory, not a symlink to the whole
	// ~/.config tree (that would expose unrelated host application configs).
	configDst := filepath.Join(agentHome, ".config")
	fi, err := os.Lstat(configDst)
	if err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error(".agent-home/.config must be a real directory, not a symlink to the entire ~/.config")
	}
	if !fi.IsDir() {
		t.Errorf(".agent-home/.config must be a directory; got mode %v", fi.Mode())
	}

	// .agent-home/.config/gh must be a symlink pointing to the real ~/.config/gh.
	ghConfigDst := filepath.Join(agentHome, ".config", "gh")
	ghFi, err := os.Lstat(ghConfigDst)
	if err != nil {
		t.Fatalf(".agent-home/.config/gh missing: %v", err)
	}
	if ghFi.Mode()&os.ModeSymlink == 0 {
		t.Error(".agent-home/.config/gh must be a symlink")
	}
	target, err := os.Readlink(ghConfigDst)
	if err != nil {
		t.Fatalf("readlink .agent-home/.config/gh: %v", err)
	}
	if target != ghConfigSrc {
		t.Errorf(".agent-home/.config/gh symlink target = %q; want %q", target, ghConfigSrc)
	}
}

// TestAgentEnv_codexSymlink verifies that agentEnv creates a symlink
// .agent-home/.codex → <HOME>/.codex when ~/.codex exists, giving the codex
// CLI access to its credentials under HOME isolation.
func TestAgentEnv_codexSymlink(t *testing.T) {
	// Build a fake HOME containing .codex
	fakeHome := t.TempDir()
	codexSrc := filepath.Join(fakeHome, ".codex")
	if err := os.MkdirAll(codexSrc, 0o755); err != nil {
		t.Fatalf("setup fake HOME: %v", err)
	}

	// Point HOME at our fake directory so os.UserHomeDir picks it up.
	t.Setenv("HOME", fakeHome)

	dir := t.TempDir()
	if _, err := agentEnv(dir); err != nil {
		t.Fatalf("agentEnv: %v", err)
	}

	agentHome := filepath.Join(dir, ".agent-home")

	// .agent-home/.codex must be a symlink pointing to the real ~/.codex.
	codexDst := filepath.Join(agentHome, ".codex")
	fi, err := os.Lstat(codexDst)
	if err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error(".agent-home/.codex must be a symlink")
	}
	target, err := os.Readlink(codexDst)
	if err != nil {
		t.Fatalf("readlink .agent-home/.codex: %v", err)
	}
	if target != codexSrc {
		t.Errorf(".agent-home/.codex symlink target = %q; want %q", target, codexSrc)
	}
}

// fakeGHBin writes a small shell script to dir/gh that outputs token when
// called as "gh auth token", and updates PATH so exec.Command("gh", …) finds it.
func fakeGHBin(t *testing.T, token string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = \"auth\" ] && [ \"$2\" = \"token\" ]; then\n  echo " + token + "\n  exit 0\nfi\nexit 1\n"
	bin := filepath.Join(dir, "gh")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("fakeGHBin: write script: %v", err)
	}
	orig := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+orig)
}

// TestAgentEnv_injectsTokenWhenAbsent verifies that GITHUB_TOKEN is added to
// the environment when it is not already present.
func TestAgentEnv_injectsTokenWhenAbsent(t *testing.T) {
	fakeGHBin(t, "test-token-injected")
	// Truly unset GITHUB_TOKEN (not just set to empty) so it is absent.
	orig, had := os.LookupEnv("GITHUB_TOKEN")
	os.Unsetenv("GITHUB_TOKEN")
	t.Cleanup(func() {
		if had {
			os.Setenv("GITHUB_TOKEN", orig)
		} else {
			os.Unsetenv("GITHUB_TOKEN")
		}
	})

	dir := t.TempDir()
	env, err := agentEnv(dir)
	if err != nil {
		t.Fatalf("agentEnv: %v", err)
	}
	for _, kv := range env {
		if kv == "GITHUB_TOKEN=test-token-injected" {
			return // found — pass
		}
	}
	t.Error("GITHUB_TOKEN=test-token-injected not found in env")
}

// TestAgentEnv_preservesExistingToken verifies that a non-empty GITHUB_TOKEN
// already present in the environment is left unchanged.
func TestAgentEnv_preservesExistingToken(t *testing.T) {
	fakeGHBin(t, "should-not-be-used")
	t.Setenv("GITHUB_TOKEN", "existing-token")

	dir := t.TempDir()
	env, err := agentEnv(dir)
	if err != nil {
		t.Fatalf("agentEnv: %v", err)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "GITHUB_TOKEN=") {
			if kv != "GITHUB_TOKEN=existing-token" {
				t.Errorf("GITHUB_TOKEN was overwritten; got %q", kv)
			}
			return
		}
	}
	t.Error("GITHUB_TOKEN not found in env at all")
}

// TestAgentEnv_replacesEmptyToken verifies that an empty GITHUB_TOKEN is
// treated as absent and replaced with the token from `gh auth token`.
func TestAgentEnv_replacesEmptyToken(t *testing.T) {
	fakeGHBin(t, "replacement-token")
	t.Setenv("GITHUB_TOKEN", "")

	dir := t.TempDir()
	env, err := agentEnv(dir)
	if err != nil {
		t.Fatalf("agentEnv: %v", err)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "GITHUB_TOKEN=") {
			if kv != "GITHUB_TOKEN=replacement-token" {
				t.Errorf("expected GITHUB_TOKEN=replacement-token, got %q", kv)
			}
			return
		}
	}
	t.Error("GITHUB_TOKEN not found in env at all")
}

func TestStreamLog_fileExists(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "agent.log")
	content := "line one\nline two\nline three\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := streamLog(dir, 50, true, &buf, 100*time.Millisecond); err != nil {
		t.Fatalf("streamLog: %v", err)
	}
	out := buf.String()
	for _, line := range []string{"line one", "line two", "line three"} {
		if !strings.Contains(out, line) {
			t.Errorf("output missing %q; got: %q", line, out)
		}
	}
}

func TestStreamLog_fileMissing(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	err := streamLog(dir, 50, true, &buf, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected error when agent.log is missing")
	}
	logPath := filepath.Join(dir, "agent.log")
	if !strings.Contains(err.Error(), logPath) {
		t.Errorf("error should contain log path %q; got: %v", logPath, err)
	}
	if !strings.Contains(err.Error(), "agent log not found") {
		t.Errorf("error should contain 'agent log not found'; got: %v", err)
	}
}

// TestStreamLog_followExitsOnProcessDeath verifies that streamLog exits with the
// guidance message when the agent process dies while following the log.
func TestStreamLog_followExitsOnProcessDeath(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "agent.log")
	if err := os.WriteFile(logPath, []byte("agent started\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Start a short-lived helper process and wait for it to exit so IsAlive
	// will return false before streamLog's first poll fires.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Skipf("could not start helper process: %v", err)
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	_ = cmd.Wait()

	// Write the (now-dead) PID to .agent so streamLog can pick it up.
	if err := state.AppendKey(dir, "agent-pid", pid); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- streamLog(dir, 50, false, &buf, 100*time.Millisecond)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("streamLog: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "agent process has exited") {
			t.Errorf("expected exit message; got: %q", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("streamLog did not return within 5s after agent process exited")
	}
}

// TestStreamLog_followExitsWhenPIDAppearsLate verifies that streamLog detects
// process death even when agent-pid is appended to .agent after streamLog starts.
func TestStreamLog_followExitsWhenPIDAppearsLate(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "agent.log")
	if err := os.WriteFile(logPath, []byte("agent started\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Start a short-lived process, capture its PID, then let it die before
	// we write it to .agent.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Skipf("could not start helper process: %v", err)
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	_ = cmd.Wait()

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- streamLog(dir, 50, false, &buf, 100*time.Millisecond)
	}()

	// Write the dead PID after streamLog has started so the re-read logic
	// inside the polling loop is exercised.
	time.Sleep(200 * time.Millisecond)
	if err := state.AppendKey(dir, "agent-pid", pid); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("streamLog: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "agent process has exited") {
			t.Errorf("expected exit message; got: %q", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("streamLog did not return within 5s after agent-pid was written")
	}
}

func TestRunLogs_unknownIssue(t *testing.T) {
	var buf bytes.Buffer
	err := runLogs("99999", 50, true, &buf)
	if err == nil {
		t.Fatal("expected error for unknown issue")
	}
	if !strings.Contains(err.Error(), "no worktree found") {
		t.Errorf("error should contain 'no worktree found'; got: %v", err)
	}
}

// ─── followLog ───────────────────────────────────────────────────────────────

// TestFollowLog_drainsContentAndExits verifies that followLog flushes all
// lines written to the log file after done is closed, and that the spinner
// escape sequences are not emitted when the writer is not a terminal.
func TestFollowLog_drainsContentAndExits(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "agent.log")

	// Write some initial content before followLog starts.
	if err := os.WriteFile(logPath, []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var buf bytes.Buffer

	// Run followLog in a goroutine so we can control timing.
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		followLog(logPath, &buf, done, false, false)
	}()

	// Poll until followLog has picked up the initial content.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(buf.String(), "line one") {
		time.Sleep(10 * time.Millisecond)
	}

	// Append more content while followLog is running.
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("line three\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	// Poll until followLog has picked up the appended line.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(buf.String(), "line three") {
		time.Sleep(10 * time.Millisecond)
	}

	// Signal done; followLog should drain any remaining content and return.
	close(done)

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("followLog did not return within timeout after done was closed")
	}

	out := buf.String()
	for _, want := range []string{"line one", "line two", "line three"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got: %q", want, out)
		}
	}
	// buf is a *bytes.Buffer, not a terminal, so ANSI spinner codes must be absent.
	if strings.Contains(out, "\r") || strings.Contains(out, "\033[K") {
		t.Errorf("unexpected ANSI escape sequences in non-terminal output: %q", out)
	}
}

// TestFollowLog_heartbeatOnNonTTY verifies that a heartbeat line is printed on
// a non-terminal writer after the 30-second threshold has elapsed.
func TestFollowLog_heartbeatOnNonTTY(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "agent.log")
	if err := os.WriteFile(logPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var buf bytes.Buffer

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		followLog(logPath, &buf, done, false, false)
	}()

	// The first heartbeat is emitted immediately (lastHeartbeat starts 30s in the
	// past), so poll until it appears.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(buf.String(), "agent running...") {
		time.Sleep(10 * time.Millisecond)
	}

	close(done)
	<-finished

	out := buf.String()
	if !strings.Contains(out, "agent running...") {
		t.Errorf("expected heartbeat line in non-terminal output; got: %q", out)
	}
}

// TestFollowLog_quietSuppressesLogLines verifies that log content is not written
// to out when quiet is true, but the heartbeat still appears.
func TestFollowLog_quietSuppressesLogLines(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "agent.log")
	if err := os.WriteFile(logPath, []byte("secret line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var buf bytes.Buffer

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		followLog(logPath, &buf, done, true, false)
	}()

	time.Sleep(300 * time.Millisecond)
	close(done)
	<-finished

	out := buf.String()
	if strings.Contains(out, "secret line") {
		t.Errorf("quiet mode should suppress log lines; got: %q", out)
	}
	if !strings.Contains(out, "agent running...") {
		t.Errorf("quiet mode should still show heartbeat; got: %q", out)
	}
}

// TestIsWriterTerminal_nonFile verifies that a *bytes.Buffer is not reported
// as a terminal.
func TestIsWriterTerminal_nonFile(t *testing.T) {
	var buf bytes.Buffer
	if isWriterTerminal(&buf) {
		t.Error("expected isWriterTerminal to return false for *bytes.Buffer")
	}
}

// ─── attachLog ────────────────────────────────────────────────────────────────

func TestAttachLog_missingPID(t *testing.T) {
	dir := t.TempDir()
	// Write a .agent file without an agent-pid key.
	if err := state.Write(dir, state.AgentFile{Agent: "claude", SessionID: "s1", DevPID: "999"}); err != nil {
		t.Fatal(err)
	}
	// Create agent.log so the wait-for-file check passes.
	if err := os.WriteFile(filepath.Join(dir, "agent.log"), []byte("log\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	err := attachLog(dir, "42", &buf, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error when agent-pid is missing")
	}
	if !strings.Contains(err.Error(), "no agent PID recorded") {
		t.Errorf("error should contain 'no agent PID recorded'; got: %v", err)
	}
}

func TestAttachLog_agentAlreadyDead(t *testing.T) {
	dir := t.TempDir()

	// Spawn a short-lived process and capture its PID after it exits.
	proc := exec.Command("true")
	if err := proc.Start(); err != nil {
		t.Fatalf("start true: %v", err)
	}
	pid := proc.Process.Pid
	_ = proc.Wait() // wait until truly dead

	// Write .agent with the dead PID.
	if err := state.Write(dir, state.AgentFile{
		Agent:     "claude",
		SessionID: "s1",
		DevPID:    "0",
		AgentPID:  strconv.Itoa(pid),
	}); err != nil {
		t.Fatal(err)
	}

	// Write an agent.log with recognisable content.
	logContent := "agent did some work\n"
	if err := os.WriteFile(filepath.Join(dir, "agent.log"), []byte(logContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := attachLog(dir, "42", &buf, 100*time.Millisecond); err != nil {
		t.Fatalf("attachLog: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "agent has already finished") {
		t.Errorf("expected 'agent has already finished' in output; got: %q", out)
	}
}

func TestAttachLog_agentRunning(t *testing.T) {
	dir := t.TempDir()

	// Spawn a real short-lived process.
	proc := exec.Command("sleep", "1")
	if err := proc.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := proc.Process.Pid
	// Reap the child asynchronously so it is promptly removed from the
	// process table and IsAlive returns false once it exits.
	go func() { _ = proc.Wait() }()

	// Write .agent with the running PID.
	if err := state.Write(dir, state.AgentFile{
		Agent:     "claude",
		SessionID: "s1",
		DevPID:    "0",
		AgentPID:  strconv.Itoa(pid),
	}); err != nil {
		t.Fatal(err)
	}

	// Write an agent.log so tail has something to read.
	if err := os.WriteFile(filepath.Join(dir, "agent.log"), []byte("starting\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	start := time.Now()
	if err := attachLog(dir, "42", &buf, 100*time.Millisecond); err != nil {
		t.Fatalf("attachLog: %v", err)
	}
	elapsed := time.Since(start)
	// The process sleeps for 1s; attachLog should return shortly after.
	if elapsed < 800*time.Millisecond {
		t.Errorf("attachLog returned too quickly (%v); expected ~1s wait", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Errorf("attachLog took too long (%v)", elapsed)
	}
}

// TestConvertStreamToLog verifies that convertStreamToLog parses stream-json
// lines and appends human-readable text to the log file without truncating
// existing content.
func TestConvertStreamToLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "agent.log")

	// Verify append semantics: write prior content first.
	if err := os.WriteFile(logPath, []byte("prior line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	assistantLine := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world"}]}}`
	toolLine := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"bash","input":{"command":"go test ./..."}}]}}`
	input := assistantLine + "\n" + toolLine + "\n"

	if err := convertStreamToLog(strings.NewReader(input), logPath, dir); err != nil {
		t.Fatalf("convertStreamToLog: %v", err)
	}

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(content)
	if !strings.Contains(got, "prior line") {
		t.Error("convertStreamToLog must append, not truncate; prior content missing")
	}
	if !strings.Contains(got, "Hello world") {
		t.Errorf("expected 'Hello world' in log:\n%s", got)
	}
	if !strings.Contains(got, "bash: go test ./...") {
		t.Errorf("expected bash tool label in log:\n%s", got)
	}
}

func TestExtractStreamText(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		wtDir string
		want  string
	}{
		{
			name: "assistant text",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"I'll fix the bug."}]}}`,
			want: "I'll fix the bug.",
		},
		{
			name: "assistant tool_use Bash with command",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls -la","description":""}}]}}`,
			want: "[Bash: ls -la]",
		},
		{
			name: "assistant tool_use Bash prefers description",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls -la","description":"List files"}}]}}`,
			want: "[Bash: List files]",
		},
		{
			name: "assistant tool_use Read with file_path",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/foo/bar.go"}}]}}`,
			want: "[Read: /foo/bar.go]",
		},
		{
			name: "assistant tool_use Write with file_path",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"/foo/bar.go"}}]}}`,
			want: "[Write: /foo/bar.go]",
		},
		{
			name: "assistant tool_use Edit with file_path",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/foo/bar.go"}}]}}`,
			want: "[Edit: /foo/bar.go]",
		},
		{
			name: "assistant tool_use WebSearch with query",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"WebSearch","input":{"query":"go cobra optional flag"}}]}}`,
			want: "[WebSearch: go cobra optional flag]",
		},
		{
			name: "assistant tool_use WebFetch with url",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"WebFetch","input":{"url":"https://pkg.go.dev/github.com/spf13/cobra"}}]}}`,
			want: "[WebFetch: https://pkg.go.dev/github.com/spf13/cobra]",
		},
		{
			name: "assistant tool_use Read missing file_path",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{}}]}}`,
			want: "[Read]",
		},
		{
			name: "assistant tool_use lowercase bash",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"bash","input":{"command":"ls -la"}}]}}`,
			want: "[bash: ls -la]",
		},
		{
			name: "assistant tool_use Bash multiline command collapsed",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls -la\necho hello"}}]}}`,
			want: "[Bash: ls -la echo hello]",
		},
		{
			name: "assistant tool_use Bash long command truncated",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"` + strings.Repeat("a", 130) + `"}}]}}`,
			want: "[Bash: " + strings.Repeat("a", 120) + "...]",
		},
		{
			name: "assistant tool_use unknown no detail",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"SomeTool","input":{}}]}}`,
			want: "[SomeTool]",
		},
		{
			name: "assistant text + tool_use",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"Running ls."},{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`,
			want: "Running ls.\n[Bash: ls]",
		},
		{
			name: "result event suppressed to avoid duplication",
			line: `{"type":"result","subtype":"success","result":"PR opened."}`,
			want: "",
		},
		{
			name: "system event skipped",
			line: `{"type":"system","subtype":"init","model":"claude-opus-4-5"}`,
			want: "",
		},
		{
			name: "user event skipped",
			line: `{"type":"user","message":{"role":"user","content":[]}}`,
			want: "",
		},
		{
			name: "non-JSON dropped",
			line: "plain text output",
			want: "",
		},
		{
			name: "empty assistant text skipped",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"   "}]}}`,
			want: "",
		},
		{
			name:  "Read path stripped to relative when under wtDir",
			line:  `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/worktree/components/Foo.tsx"}}]}}`,
			wtDir: "/worktree",
			want:  "[Read: components/Foo.tsx]",
		},
		{
			name:  "Write path stripped to relative when under wtDir",
			line:  `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"/worktree/lib/bar.ts"}}]}}`,
			wtDir: "/worktree",
			want:  "[Write: lib/bar.ts]",
		},
		{
			name:  "Edit path stripped to relative when under wtDir",
			line:  `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/worktree/app/page.tsx"}}]}}`,
			wtDir: "/worktree",
			want:  "[Edit: app/page.tsx]",
		},
		{
			name:  "Read path outside wtDir shown as-is",
			line:  `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/other/file.go"}}]}}`,
			wtDir: "/worktree",
			want:  "[Read: /other/file.go]",
		},
		{
			name:  "Read path with empty wtDir shown as-is",
			line:  `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/some/path.go"}}]}}`,
			wtDir: "",
			want:  "[Read: /some/path.go]",
		},
		{
			name: "assistant text PR URL gets visual separation",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"https://github.com/arun-gupta/agentctl/pull/135"}]}}`,
			want: "\n>>> https://github.com/arun-gupta/agentctl/pull/135\n",
		},
		{
			name: "assistant text PR URL embedded in sentence",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"PR opened at https://github.com/arun-gupta/agentctl/pull/135 please review"}]}}`,
			want: "PR opened at \n>>> https://github.com/arun-gupta/agentctl/pull/135\n please review",
		},
		{
			name: "assistant text non-PR GitHub URL unchanged",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"See https://github.com/arun-gupta/agentctl/issues/137"}]}}`,
			want: "See https://github.com/arun-gupta/agentctl/issues/137",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractStreamText(tc.line, tc.wtDir)
			if got != tc.want {
				t.Errorf("extractStreamText(%q) = %q, want %q", tc.line, got, tc.want)
			}
		})
	}
}

func TestToolLabel_NoToolDetail(t *testing.T) {
	t.Setenv("AGENTCTL_NO_TOOL_DETAIL", "1")
	got := toolLabel("Bash", json.RawMessage(`{"command":"ls -la"}`), "")
	if got != "Bash" {
		t.Errorf("expected %q with AGENTCTL_NO_TOOL_DETAIL set, got %q", "Bash", got)
	}
}

func TestSanitizeDetail(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "hello world", "hello world"},
		{"trim spaces", "  hello  ", "hello"},
		{"collapse newline", "ls -la\necho hi", "ls -la echo hi"},
		{"collapse tab", "a\tb", "a b"},
		{"collapse multiple spaces", "a   b", "a b"},
		{"truncate at 120 runes", strings.Repeat("x", 130), strings.Repeat("x", 120) + "..."},
		{"exact 120 runes no ellipsis", strings.Repeat("x", 120), strings.Repeat("x", 120)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeDetail(tc.input)
			if got != tc.want {
				t.Errorf("sanitizeDetail(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestStartCmd_sddFlagRequiresExplicitValue(t *testing.T) {
	c := NewStartCmd()
	f := c.Flags().Lookup("sdd")
	if f == nil {
		t.Fatal("--sdd flag not found")
	}
	if f.NoOptDefVal != "" {
		t.Errorf("--sdd should require an explicit value (NoOptDefVal must be empty), got %q", f.NoOptDefVal)
	}
}

// ─── worktreeExistsError ──────────────────────────────────────────────────────

// TestWorktreeExistsError_runningAgent verifies the error message when the
// worktree already exists and the agent process is still alive.
func TestWorktreeExistsError_runningAgent(t *testing.T) {
	dir := t.TempDir()
	alivePID := strconv.Itoa(os.Getpid()) // current process is definitely alive
	if err := state.Write(dir, state.AgentFile{
		Agent:     "claude",
		SessionID: "sess-1",
		DevPID:    "999",
		AgentPID:  alivePID,
	}); err != nil {
		t.Fatal(err)
	}

	err := worktreeExistsError(dir, "90")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Worktree already exists for issue 90") {
		t.Errorf("expected 'Worktree already exists for issue 90' in error; got: %q", msg)
	}
	if !strings.Contains(msg, "agent is still running") {
		t.Errorf("expected 'agent is still running' in error; got: %q", msg)
	}
	if !strings.Contains(msg, "agentctl attach 90") {
		t.Errorf("expected 'agentctl attach 90' hint in error; got: %q", msg)
	}
	if !strings.Contains(msg, "agentctl discard 90") {
		t.Errorf("expected 'agentctl discard 90' hint in error; got: %q", msg)
	}
	if !strings.Contains(msg, dir) {
		t.Errorf("expected worktree path %q in error; got: %q", dir, msg)
	}
}

// TestWorktreeExistsError_finishedAgent verifies the error message when the
// worktree already exists and the agent process is no longer running.
func TestWorktreeExistsError_finishedAgent(t *testing.T) {
	dir := t.TempDir()
	deadPID := "9999999" // very unlikely to be a live process
	if err := state.Write(dir, state.AgentFile{
		Agent:     "claude",
		SessionID: "sess-2",
		DevPID:    "999",
		AgentPID:  deadPID,
	}); err != nil {
		t.Fatal(err)
	}

	err := worktreeExistsError(dir, "90")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Worktree already exists for issue 90") {
		t.Errorf("expected 'Worktree already exists for issue 90' in error; got: %q", msg)
	}
	if !strings.Contains(msg, "agent has finished") {
		t.Errorf("expected 'agent has finished' in error; got: %q", msg)
	}
	if !strings.Contains(msg, "agentctl cleanup 90") {
		t.Errorf("expected 'agentctl cleanup 90' hint in error; got: %q", msg)
	}
	if !strings.Contains(msg, "agentctl discard 90") {
		t.Errorf("expected 'agentctl discard 90' hint in error; got: %q", msg)
	}
	if !strings.Contains(msg, dir) {
		t.Errorf("expected worktree path %q in error; got: %q", dir, msg)
	}
}

// TestWorktreeExistsError_noAgentFile verifies the error message when the
// worktree already exists but there is no .agent metadata file.
func TestWorktreeExistsError_noAgentFile(t *testing.T) {
	dir := t.TempDir()
	// No .agent file written — directory exists but is otherwise empty.

	err := worktreeExistsError(dir, "90")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Worktree already exists for issue 90") {
		t.Errorf("expected 'Worktree already exists for issue 90' in error; got: %q", msg)
	}
	if !strings.Contains(msg, "agentctl discard 90") {
		t.Errorf("expected 'agentctl discard 90' hint in error; got: %q", msg)
	}
	if !strings.Contains(msg, dir) {
		t.Errorf("expected worktree path %q in error; got: %q", dir, msg)
	}
}

func TestSeedEnvLocal_missingSource_doesNothing(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, ".env.local")
	if err := seedEnvLocal(filepath.Join(dir, "nonexistent"), dst); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("expected dst to not exist, but it does")
	}
}

func TestSeedEnvLocal_copiesAndStripsPort(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env.local")
	dst := filepath.Join(dir, "dst.env.local")
	content := "API_KEY=secret\nPORT=3000\nDATABASE_URL=postgres://localhost/db\nPORT=4000\n"
	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedEnvLocal(src, dst); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("could not read dst: %v", err)
	}
	result := string(got)
	if strings.Contains(result, "PORT=") {
		t.Errorf("PORT= lines should be stripped, got: %q", result)
	}
	if !strings.Contains(result, "API_KEY=secret") {
		t.Errorf("API_KEY should be preserved, got: %q", result)
	}
	if !strings.Contains(result, "DATABASE_URL=postgres://localhost/db") {
		t.Errorf("DATABASE_URL should be preserved, got: %q", result)
	}
}

func TestStartDevServer_noPackageJSON_returnsEmptyPort(t *testing.T) {
	dir := t.TempDir() // no .agentctl.yml
	var buf strings.Builder
	pid, port, err := startDevServer(dir, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pid != "" {
		t.Errorf("expected empty pid when no dev server started, got %q", pid)
	}
	if port != "" {
		t.Errorf("expected empty port when no dev server started, got %q", port)
	}
	if buf.String() != "" {
		t.Errorf("expected no output when dev_server is not configured, got %q", buf.String())
	}
}

// ─── isStderrNoise ────────────────────────────────────────────────────────────

func TestIsStderrNoise(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		// JavaScript/Node.js stack frames
		{
			name: "stack frame 4 spaces",
			line: "    at Gaxios._request (file:///opt/homebrew/Cellar/gemini-cli/bundle/chunk.js:8578:19)",
			want: true,
		},
		{
			name: "stack frame node internals",
			line: "    at process.processTicksAndRejections (node:internal/process/task_queues:104:5)",
			want: true,
		},
		{
			name: "stack frame 2 spaces",
			line: "  at foo (bar.ts:1:2)",
			want: true,
		},
		{
			name: "stack frame tab-indented",
			line: "\tat SomeClass.method (file.js:10:5)",
			want: true,
		},
		// Raw JSON blobs
		{
			name: "json object blob",
			line: `{"error":{"code":429,"message":"No capacity"}}`,
			want: true,
		},
		{
			name: "json array blob",
			line: `[{"error":"something"}]`,
			want: true,
		},
		{
			name: "json object with leading whitespace",
			line: `  {"code": 429}`,
			want: true,
		},
		// Not noise — should pass through
		{
			name: "human-readable error summary",
			line: "Attempt 2 failed with status 429. Retrying with backoff...",
			want: false,
		},
		{
			name: "normal agent output",
			line: "Starting agent...",
			want: false,
		},
		{
			name: "empty line",
			line: "",
			want: false,
		},
		{
			name: "incomplete json not a blob",
			line: `{"type": "assistant"`,
			want: false,
		},
		{
			name: "plain text that mentions at",
			line: "looking at the problem",
			want: false,
		},
		{
			name: "json fragment property line",
			line: `  "error": {`,
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isStderrNoise(tc.line)
			if got != tc.want {
				t.Errorf("isStderrNoise(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

// TestFollowLog_filterNoise_suppressesStackTracesAndJsonBlobs verifies that
// when filterNoise is true, followLog omits stack frames and JSON blobs but
// still shows normal lines. The raw content remains in the file.
func TestFollowLog_filterNoise_suppressesStackTracesAndJsonBlobs(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "agent.log")

	content := strings.Join([]string{
		"Starting agent",
		`    at Gaxios._request (file:///path/to/bundle.js:8578:19)`,
		`    at process.processTicksAndRejections (node:internal/process/task_queues:104:5)`,
		`{"error":{"code":429,"message":"No capacity"}}`,
		"normal output after error",
		"",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var buf bytes.Buffer
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		followLog(logPath, &buf, done, false, true)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(buf.String(), "normal output") {
		time.Sleep(10 * time.Millisecond)
	}

	close(done)
	<-finished

	out := buf.String()
	if strings.Contains(out, "at Gaxios._request") {
		t.Errorf("stack frame should be suppressed; got: %q", out)
	}
	if strings.Contains(out, `"code":429`) {
		t.Errorf("JSON blob should be suppressed; got: %q", out)
	}
	if !strings.Contains(out, "Starting agent") {
		t.Errorf("normal line should pass through; got: %q", out)
	}
	if !strings.Contains(out, "normal output after error") {
		t.Errorf("normal line should pass through; got: %q", out)
	}
}

// TestFollowLog_filterNoise_false_passesEverything verifies that when
// filterNoise is false, followLog streams everything including stack frames.
func TestFollowLog_filterNoise_false_passesEverything(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "agent.log")

	content := "normal line\n    at Gaxios._request (file:///path.js:1:1)\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var buf bytes.Buffer
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		followLog(logPath, &buf, done, false, false)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(buf.String(), "at Gaxios") {
		time.Sleep(10 * time.Millisecond)
	}

	close(done)
	<-finished

	out := buf.String()
	if !strings.Contains(out, "at Gaxios._request") {
		t.Errorf("without filterNoise, stack frame should be present; got: %q", out)
	}
}

func TestStartDevServer_agentctlYml_writesPortBack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".agentctl.yml"),
		[]byte("dev_server: \"echo ok\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pidStr, portStr, err := startDevServer(dir, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pidStr != "" {
		t.Cleanup(func() {
			pid, parseErr := strconv.Atoi(pidStr)
			if parseErr != nil {
				t.Logf("cleanup: could not parse dev server pid %q: %v", pidStr, parseErr)
				return
			}
			proc, findErr := os.FindProcess(pid)
			if findErr != nil {
				t.Logf("cleanup: could not find dev server process %d: %v", pid, findErr)
				return
			}
			_ = proc.Kill()
			_, _ = proc.Wait()
		})
	}
	if portStr == "" {
		t.Fatal("expected non-empty port when dev_server is set")
	}
	// Verify port was written back to .agentctl.yml.
	cfg, err := config.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%d", cfg.Port) != portStr {
		t.Errorf("port in .agentctl.yml = %d, want %s", cfg.Port, portStr)
	}
}

// ─── writeClaudeSettings ──────────────────────────────────────────────────────

func TestWriteClaudeSettings_createsFile(t *testing.T) {
	dir := t.TempDir()
	if err := writeClaudeSettings(dir); err != nil {
		t.Fatalf("writeClaudeSettings: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf(".claude/settings.json not created: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal settings.json: %v", err)
	}
	perms, ok := got["permissions"].(map[string]any)
	if !ok {
		t.Fatal("settings.json missing 'permissions' object")
	}
	allow, ok := perms["allow"].([]any)
	if !ok || len(allow) == 0 || allow[0] != "*" {
		t.Errorf("permissions.allow = %v, want [\"*\"]", allow)
	}
}

func TestWriteClaudeSettings_doesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"custom":"value"}`)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeClaudeSettings(dir); err != nil {
		t.Fatalf("writeClaudeSettings: %v", err)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(original) {
		t.Errorf("existing settings.json was overwritten: got %q, want %q", data, original)
	}
}

func TestLaunchAgent_claudeHeadlessWritesSettingsJson(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "claude-stub")
	// The stub exits immediately without writing any files to the temp dir.
	// Previous versions wrote argv.txt here, but that file is never read by
	// this test and its asynchronous creation caused a temp-dir cleanup race
	// on Linux CI (shell started after RemoveAll, creating a file in a
	// directory that was being concurrently removed).
	script := "#!/bin/sh\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLocalAdapter(t, dir, "claude", "binary: "+scriptPath+"\nsession: --session\n")
	chdirTemp(t, dir)

	if err := launchAgent("claude", dir, "42", "3010", "sess-abc", "kickoff text", "", true, false, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("launchAgent headless: %v", err)
	}

	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf(".claude/settings.json not found after claude launch: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal settings.json: %v", err)
	}
	perms, ok := got["permissions"].(map[string]any)
	if !ok {
		t.Fatal("settings.json missing 'permissions'")
	}
	allow, ok := perms["allow"].([]any)
	if !ok || len(allow) == 0 || allow[0] != "*" {
		t.Errorf("permissions.allow = %v, want [\"*\"]", allow)
	}
}

func TestLaunchAgent_nonClaudeDoesNotWriteSettingsJson(t *testing.T) {
	dir := t.TempDir()
	writeLocalAdapter(t, dir, "echoagent", "binary: echo\nsession: --session\n")
	chdirTemp(t, dir)

	if err := launchAgent("echoagent", dir, "42", "3010", "sess-abc", "do the thing", "", true, false, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("launchAgent headless: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); err == nil {
		t.Error("non-claude adapter must not write .claude/settings.json")
	}
}

// ─── linkPRToIssue ────────────────────────────────────────────────────────────

// makeGHStub writes a stub gh script to stubDir and returns the path to the
// calls log file where the stub records each invocation (one arg per line).
// viewJSON is written as the stdout for "gh pr view"; pass "" to simulate a
// missing PR (gh exits 1).  listJSON is written as the stdout for "gh pr list";
// pass "" to return an empty array (no PRs, gh exits 0).  For "gh pr edit" the
// stub always exits 0 unless editFail is true.
func makeGHStub(t *testing.T, stubDir, viewJSON, listJSON string, editFail bool) string {
	t.Helper()
	callsFile := filepath.Join(stubDir, "gh-calls.txt")
	responseFile := filepath.Join(stubDir, "gh-response.json")
	listFile := filepath.Join(stubDir, "gh-list.json")
	if viewJSON != "" {
		if err := os.WriteFile(responseFile, []byte(viewJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	listOut := "[]"
	if listJSON != "" {
		listOut = listJSON
	}
	if err := os.WriteFile(listFile, []byte(listOut), 0o644); err != nil {
		t.Fatal(err)
	}

	prViewExit := 0
	if viewJSON == "" {
		prViewExit = 1
	}
	prEditExit := 0
	if editFail {
		prEditExit = 1
	}

	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" >> %s
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
  if [ -f %s ]; then cat %s; fi
  exit %d
fi
if [ "$1" = "pr" ] && [ "$2" = "list" ]; then
  cat %s
  exit 0
fi
if [ "$1" = "pr" ] && [ "$2" = "edit" ]; then
  exit %d
fi
exit 0`,
		callsFile,
		responseFile, responseFile,
		prViewExit,
		listFile,
		prEditExit,
	)
	ghPath := filepath.Join(stubDir, "gh")
	if err := os.WriteFile(ghPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return callsFile
}

// prependPath prepends dir to the PATH for the duration of the test.
func prependPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestLinkPRToIssue_noPR(t *testing.T) {
	stubDir := t.TempDir()
	callsFile := makeGHStub(t, stubDir, "", "", false)
	prependPath(t, stubDir)

	pr, err := linkPRToIssue(t.TempDir(), "42-my-feature", "42")
	if err != nil {
		t.Errorf("expected nil error for no-PR case, got: %v", err)
	}
	if pr != nil {
		t.Errorf("expected nil prInfo for no-PR case, got: %+v", pr)
	}

	calls, _ := os.ReadFile(callsFile)
	callsStr := string(calls)
	if !strings.Contains(callsStr, "pr") {
		t.Error("expected gh pr view to be called")
	}
	if strings.Contains(callsStr, "edit") {
		t.Error("expected gh pr edit NOT to be called when no PR exists")
	}
}

func TestLinkPRToIssue_alreadyLinked(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"closes_lower", "some work\n\ncloses #42"},
		{"Closes_title", "some work\n\nCloses #42"},
		{"CLOSES_upper", "some work\n\nCLOSES #42"},
		{"fixes_lower", "some work\n\nfixes #42"},
		{"Fixes_title", "some work\n\nFixes #42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubDir := t.TempDir()
			viewJSON := fmt.Sprintf(`{"number":7,"body":%q}`, tc.body)
			callsFile := makeGHStub(t, stubDir, viewJSON, "", false)
			prependPath(t, stubDir)

			if _, err := linkPRToIssue(t.TempDir(), "42-feature", "42"); err != nil {
				t.Errorf("expected nil, got: %v", err)
			}
			calls, _ := os.ReadFile(callsFile)
			if strings.Contains(string(calls), "edit") {
				t.Errorf("should not call gh pr edit when closing keyword already present; calls: %q", string(calls))
			}
		})
	}
}

func TestLinkPRToIssue_addsLink(t *testing.T) {
	stubDir := t.TempDir()
	viewJSON := `{"number":7,"body":"some PR work","url":"https://github.com/owner/repo/pull/7"}`
	callsFile := makeGHStub(t, stubDir, viewJSON, "", false)
	prependPath(t, stubDir)

	pr, err := linkPRToIssue(t.TempDir(), "42-feature", "42")
	if err != nil {
		t.Fatalf("linkPRToIssue: %v", err)
	}
	if pr == nil || pr.URL != "https://github.com/owner/repo/pull/7" {
		t.Errorf("expected PR URL to be returned, got: %+v", pr)
	}

	calls, _ := os.ReadFile(callsFile)
	callsStr := string(calls)
	if !strings.Contains(callsStr, "edit") {
		t.Error("expected gh pr edit to be called")
	}
	if !strings.Contains(callsStr, "Closes #42") {
		t.Errorf("expected 'Closes #42' in gh pr edit args, got: %q", callsStr)
	}
}

func TestLinkPRToIssue_addsLink_emptyBody(t *testing.T) {
	stubDir := t.TempDir()
	viewJSON := `{"number":3,"body":"","url":"https://github.com/owner/repo/pull/3"}`
	callsFile := makeGHStub(t, stubDir, viewJSON, "", false)
	prependPath(t, stubDir)

	if _, err := linkPRToIssue(t.TempDir(), "42-feature", "42"); err != nil {
		t.Fatalf("linkPRToIssue: %v", err)
	}

	calls, _ := os.ReadFile(callsFile)
	callsStr := string(calls)
	if !strings.Contains(callsStr, "Closes #42") {
		t.Errorf("expected 'Closes #42' in edit args, got: %q", callsStr)
	}
}

func TestLinkPRToIssue_editError(t *testing.T) {
	stubDir := t.TempDir()
	viewJSON := `{"number":7,"body":"some work","url":"https://github.com/owner/repo/pull/7"}`
	makeGHStub(t, stubDir, viewJSON, "", true) // editFail=true
	prependPath(t, stubDir)

	_, err := linkPRToIssue(t.TempDir(), "42-feature", "42")
	if err == nil {
		t.Error("expected error when gh pr edit fails")
	}
	if !strings.Contains(err.Error(), "gh pr edit") {
		t.Errorf("expected 'gh pr edit' in error, got: %v", err)
	}
}

func TestReportPRStatus_printsPRLink(t *testing.T) {
	stubDir := t.TempDir()
	viewJSON := `{"number":9,"body":"work","url":"https://github.com/owner/repo/pull/9"}`
	makeGHStub(t, stubDir, viewJSON, "", false)
	prependPath(t, stubDir)

	var buf bytes.Buffer
	reportPRStatus(&buf, t.TempDir(), "2-my-feature", "2")

	out := buf.String()
	if !strings.Contains(out, "PR: #9") {
		t.Errorf("expected PR number in output, got: %q", out)
	}
	if !strings.Contains(out, "https://github.com/owner/repo/pull/9") {
		t.Errorf("expected PR URL in output, got: %q", out)
	}
}

func TestReportPRStatus_noPR_printsNothing(t *testing.T) {
	stubDir := t.TempDir()
	makeGHStub(t, stubDir, "", "", false) // no PR
	prependPath(t, stubDir)

	var buf bytes.Buffer
	reportPRStatus(&buf, t.TempDir(), "2-my-feature", "2")

	if buf.Len() > 0 {
		t.Errorf("expected no output when no PR exists, got: %q", buf.String())
	}
}

// ─── discard --stale ─────────────────────────────────────────────────────────

func TestDiscardCmd_staleFlagRegistered(t *testing.T) {
	c := NewDiscardCmd()
	if f := c.Flags().Lookup("stale"); f == nil {
		t.Fatal("--stale flag must be registered on the discard command")
	}
}

func TestDiscardCmd_staleMutuallyExclusiveWithIssue(t *testing.T) {
	c := NewDiscardCmd()
	var errBuf bytes.Buffer
	c.SetErr(&errBuf)
	c.SilenceUsage = true
	c.SetArgs([]string{"--stale", "42"})
	err := c.Execute()
	if err == nil {
		t.Fatal("expected error when --stale and issue number are both provided")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error should mention mutually exclusive, got: %v", err)
	}
}

func TestIsAgentRunning_noAgentFile(t *testing.T) {
	dir := t.TempDir()
	if isAgentRunning(dir) {
		t.Error("expected isAgentRunning=false when no .agent file exists")
	}
}

func TestIsAgentRunning_emptyPID(t *testing.T) {
	dir := t.TempDir()
	if err := state.Write(dir, state.AgentFile{Agent: "claude", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if isAgentRunning(dir) {
		t.Error("expected isAgentRunning=false when AgentPID is empty")
	}
}

func TestIsAgentRunning_deadPID(t *testing.T) {
	dir := t.TempDir()
	if err := state.Write(dir, state.AgentFile{Agent: "claude", AgentPID: "9999999"}); err != nil {
		t.Fatal(err)
	}
	if isAgentRunning(dir) {
		t.Error("expected isAgentRunning=false for a dead PID")
	}
}

func TestIsAgentRunning_livePID(t *testing.T) {
	dir := t.TempDir()
	pid := strconv.Itoa(os.Getpid())
	if err := state.Write(dir, state.AgentFile{Agent: "claude", AgentPID: pid}); err != nil {
		t.Fatal(err)
	}
	if !isAgentRunning(dir) {
		t.Error("expected isAgentRunning=true when AgentPID is the current process")
	}
}

func TestIsAgentRunning_unreadableFile(t *testing.T) {
	dir := t.TempDir()
	// Write an .agent file then make it unreadable.
	agentFile := filepath.Join(dir, ".agent")
	if err := os.WriteFile(agentFile, []byte("agent-pid=99999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(agentFile, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(agentFile, 0o600) })
	// Should return true conservatively when file is unreadable.
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	if !isAgentRunning(dir) {
		t.Error("expected isAgentRunning=true when .agent file is unreadable (conservative)")
	}
}

// ─── ghHasPR ─────────────────────────────────────────────────────────────────

func TestGhHasPR_noPR(t *testing.T) {
	stubDir := t.TempDir()
	makeGHStub(t, stubDir, "", "", false) // listJSON="" → returns []
	prependPath(t, stubDir)

	has, err := ghHasPR(t.TempDir(), "42-my-feature")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if has {
		t.Error("expected ghHasPR=false when pr list returns []")
	}
}

func TestGhHasPR_hasPR(t *testing.T) {
	stubDir := t.TempDir()
	listJSON := `[{"number":7}]`
	makeGHStub(t, stubDir, "", listJSON, false)
	prependPath(t, stubDir)

	has, err := ghHasPR(t.TempDir(), "42-my-feature")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !has {
		t.Error("expected ghHasPR=true when pr list returns a non-empty array")
	}
}

func TestGhHasPR_ghError(t *testing.T) {
	// Use a stub that exits non-zero for pr list to simulate auth/network failure.
	stubDir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "pr" ] && [ "$2" = "list" ]; then
  echo "error: not authenticated" >&2
  exit 1
fi
exit 0`)
	ghPath := filepath.Join(stubDir, "gh")
	if err := os.WriteFile(ghPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	prependPath(t, stubDir)

	has, err := ghHasPR(t.TempDir(), "42-my-feature")
	if err == nil {
		t.Fatal("expected error when gh exits non-zero")
	}
	if has {
		t.Error("expected ghHasPR=false on error")
	}
}

// ─── isStaleWorktree ─────────────────────────────────────────────────────────

func TestIsStaleWorktree_agentRunning(t *testing.T) {
	dir := t.TempDir()
	pid := strconv.Itoa(os.Getpid())
	if err := state.Write(dir, state.AgentFile{Agent: "claude", AgentPID: pid}); err != nil {
		t.Fatal(err)
	}
	// No gh stub needed; isAgentRunning short-circuits.
	if isStaleWorktree(t.TempDir(), "42-my-feature", dir) {
		t.Error("expected isStaleWorktree=false when agent is running")
	}
}

func TestIsStaleWorktree_hasPR(t *testing.T) {
	dir := t.TempDir() // no .agent file → agent not running
	stubDir := t.TempDir()
	listJSON := `[{"number":7}]`
	makeGHStub(t, stubDir, "", listJSON, false)
	prependPath(t, stubDir)

	if isStaleWorktree(t.TempDir(), "42-my-feature", dir) {
		t.Error("expected isStaleWorktree=false when a PR exists")
	}
}

func TestIsStaleWorktree_noPRNoAgent(t *testing.T) {
	dir := t.TempDir() // no .agent file → agent not running
	stubDir := t.TempDir()
	makeGHStub(t, stubDir, "", "", false) // listJSON="" → returns []
	prependPath(t, stubDir)

	if !isStaleWorktree(t.TempDir(), "42-my-feature", dir) {
		t.Error("expected isStaleWorktree=true when no agent and no PR")
	}
}

func TestIsStaleWorktree_ghError(t *testing.T) {
	dir := t.TempDir() // no .agent file → agent not running
	stubDir := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = "pr" ] && [ "$2" = "list" ]; then
  echo "error: not authenticated" >&2
  exit 1
fi
exit 0`
	ghPath := filepath.Join(stubDir, "gh")
	if err := os.WriteFile(ghPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	prependPath(t, stubDir)

	// On gh error, should return false conservatively.
	if isStaleWorktree(t.TempDir(), "42-my-feature", dir) {
		t.Error("expected isStaleWorktree=false when gh returns an error (conservative)")
	}
}

// ─── runDiscardStale integration ─────────────────────────────────────────────

// initGitRepoForStale creates a temporary git repository with an initial
// commit and returns its path. Tests that need this are skipped when git is
// not available.
func initGitRepoForStale(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := t.TempDir()
	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitRun("init")
	gitRun("config", "user.email", "test@example.com")
	gitRun("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", ".")
	gitRun("commit", "-m", "init")
	return dir
}

// pipeStdin replaces os.Stdin with a pipe that delivers text for the duration
// of the test, then restores the original os.Stdin via t.Cleanup.
func pipeStdin(t *testing.T, text string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		r.Close()
	})
	if _, err := fmt.Fprintln(w, text); err != nil {
		t.Fatal(err)
	}
	w.Close()
}

// addWorktree creates a linked worktree at wtPath on branch branchName.
func addWorktree(t *testing.T, repo, wtPath, branchName string) {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "worktree", "add", wtPath, "-b", branchName)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
}

func TestRunDiscardStale_noStaleWorktrees_agentRunning(t *testing.T) {
	repo := initGitRepoForStale(t)
	wtPath := filepath.Join(t.TempDir(), "42-my-feature")
	addWorktree(t, repo, wtPath, "42-my-feature")

	// Write .agent file with current PID so agent appears running.
	pid := strconv.Itoa(os.Getpid())
	if err := state.Write(wtPath, state.AgentFile{Agent: "claude", AgentPID: pid}); err != nil {
		t.Fatal(err)
	}

	// gh stub: no PRs — but agent is running, so worktree is not stale.
	stubDir := t.TempDir()
	makeGHStub(t, stubDir, "", "", false)
	prependPath(t, stubDir)

	chdirTemp(t, repo)
	if err := runDiscardStale(); err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}

	// Worktree must still exist.
	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("worktree should still exist when agent is running: %v", err)
	}
}

func TestRunDiscardStale_noStaleWorktrees_hasPR(t *testing.T) {
	repo := initGitRepoForStale(t)
	wtPath := filepath.Join(t.TempDir(), "42-my-feature")
	addWorktree(t, repo, wtPath, "42-my-feature")

	// gh stub: pr list returns a PR → not stale.
	stubDir := t.TempDir()
	makeGHStub(t, stubDir, "", `[{"number":7}]`, false)
	prependPath(t, stubDir)

	chdirTemp(t, repo)
	if err := runDiscardStale(); err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}

	// Worktree must still exist.
	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("worktree should still exist when PR exists: %v", err)
	}
}

func TestRunDiscardStale_confirmationAbort(t *testing.T) {
	repo := initGitRepoForStale(t)
	wtPath := filepath.Join(t.TempDir(), "42-my-feature")
	addWorktree(t, repo, wtPath, "42-my-feature")

	// gh stub: no PRs → stale.
	stubDir := t.TempDir()
	makeGHStub(t, stubDir, "", "", false)
	prependPath(t, stubDir)

	chdirTemp(t, repo)
	pipeStdin(t, "n")

	err := runDiscardStale()
	if err == nil {
		t.Fatal("expected aborted error when user declines confirmation")
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Errorf("expected 'aborted' in error, got: %v", err)
	}

	// Worktree must still exist — nothing should have been removed.
	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("worktree should still exist after abort: %v", err)
	}
}

func TestRunDiscardStale_confirmationYES_removesWorktreeAndBranch(t *testing.T) {
	repo := initGitRepoForStale(t)

	// Create a bare repo as origin. The branch has not been pushed, so
	// git push origin --delete will report "remote ref does not exist",
	// which discardStaleEntry treats as "already removed" (success).
	originDir := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(originDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = originDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "-C", repo, "remote", "add", "origin", originDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}

	wtPath := filepath.Join(t.TempDir(), "42-my-feature")
	addWorktree(t, repo, wtPath, "42-my-feature")

	// gh stub: no PRs → stale.
	stubDir := t.TempDir()
	makeGHStub(t, stubDir, "", "", false)
	prependPath(t, stubDir)

	chdirTemp(t, repo)
	pipeStdin(t, "YES")

	if err := runDiscardStale(); err != nil {
		t.Fatalf("runDiscardStale: %v", err)
	}

	// Worktree directory must be gone.
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Error("worktree dir should be removed after YES confirmation")
	}

	// Local branch must be gone.
	cmd = exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/42-my-feature")
	if err := cmd.Run(); err == nil {
		t.Error("local branch should be deleted after YES confirmation")
	}
}

// ─── batch start ──────────────────────────────────────────────────────────────

// TestStartCmd_slugWithMultipleIssues verifies that providing a [slug] argument
// together with a comma-separated issue list returns an error immediately,
// before any provisioning takes place.
func TestStartCmd_slugWithMultipleIssues(t *testing.T) {
	c := NewStartCmd()
	c.SilenceUsage = true
	c.SetArgs([]string{"42,43,44", "my-slug"})
	err := c.Execute()
	if err == nil {
		t.Fatal("expected error when slug given with multiple issues")
	}
	if !strings.Contains(err.Error(), "[slug] argument is not supported when starting multiple issues") {
		t.Errorf("wrong error message: %v", err)
	}
}

// TestRunBatch_allSucceed verifies that runBatch calls startOne for every issue
// and returns nil when all succeed.
func TestRunBatch_allSucceed(t *testing.T) {
	var mu sync.Mutex
	called := map[string]bool{}

	mockFn := func(issue, slug, agentName, sddName string, headless, quiet, sendNotify bool, out io.Writer) error {
		mu.Lock()
		called[issue] = true
		mu.Unlock()
		fmt.Fprintf(out, "started %s\n", issue)
		return nil
	}

	var out, errOut bytes.Buffer
	if err := runBatch([]string{"42", "43", "44"}, "claude", "", false, false, mockFn, &out, &errOut); err != nil {
		t.Fatalf("runBatch: %v", err)
	}

	for _, iss := range []string{"42", "43", "44"} {
		if !called[iss] {
			t.Errorf("issue %s was not started", iss)
		}
	}
	outStr := out.String()
	for _, iss := range []string{"42", "43", "44"} {
		if !strings.Contains(outStr, "started "+iss) {
			t.Errorf("output missing 'started %s'; got: %q", iss, outStr)
		}
	}
}

// TestRunBatch_oneFailureContinuesOthers verifies that a failure provisioning
// one issue does not prevent the remaining issues from being started, and that
// the overall error message mentions all failures.
func TestRunBatch_oneFailureContinuesOthers(t *testing.T) {
	var mu sync.Mutex
	called := map[string]bool{}

	mockFn := func(issue, slug, agentName, sddName string, headless, quiet, sendNotify bool, out io.Writer) error {
		mu.Lock()
		called[issue] = true
		mu.Unlock()
		if issue == "43" {
			return fmt.Errorf("worktree already exists for issue 43")
		}
		fmt.Fprintf(out, "started %s\n", issue)
		return nil
	}

	var out, errOut bytes.Buffer
	err := runBatch([]string{"42", "43", "44"}, "claude", "", false, false, mockFn, &out, &errOut)
	if err == nil {
		t.Fatal("expected error when one issue fails")
	}
	if !strings.Contains(err.Error(), "one or more issues failed to start") {
		t.Errorf("wrong error: %v", err)
	}

	// All three issues must have been attempted.
	for _, iss := range []string{"42", "43", "44"} {
		if !called[iss] {
			t.Errorf("issue %s was not attempted", iss)
		}
	}

	// Successful issues must appear in combined output.
	outStr := out.String()
	for _, iss := range []string{"42", "44"} {
		if !strings.Contains(outStr, "started "+iss) {
			t.Errorf("output missing 'started %s'; got: %q", iss, outStr)
		}
	}

	// The failed issue's error must be prefixed with the issue number.
	errStr := errOut.String()
	if !strings.Contains(errStr, "[43] error:") {
		t.Errorf("error output must include '[43] error:' prefix; got: %q", errStr)
	}
	if !strings.Contains(errStr, "worktree already exists for issue 43") {
		t.Errorf("error output must include the original error message; got: %q", errStr)
	}
}

// TestRunBatch_resultsInOrder verifies that output is printed in the original
// issue order even when goroutines finish out of order.
func TestRunBatch_resultsInOrder(t *testing.T) {
	mockFn := func(issue, slug, agentName, sddName string, headless, quiet, sendNotify bool, out io.Writer) error {
		if issue == "42" {
			time.Sleep(50 * time.Millisecond) // finish last
		}
		fmt.Fprintf(out, "[%s]", issue)
		return nil
	}

	var out, errOut bytes.Buffer
	if err := runBatch([]string{"42", "43", "44"}, "claude", "", false, false, mockFn, &out, &errOut); err != nil {
		t.Fatalf("runBatch: %v", err)
	}

	outStr := out.String()
	idx42 := strings.Index(outStr, "[42]")
	idx43 := strings.Index(outStr, "[43]")
	idx44 := strings.Index(outStr, "[44]")
	if idx42 < 0 || idx43 < 0 || idx44 < 0 {
		t.Fatalf("missing issue in output: %q", outStr)
	}
	if !(idx42 < idx43 && idx43 < idx44) {
		t.Errorf("output not in issue order; got: %q", outStr)
	}
}

// TestStartCmd_emptyIssueTokens verifies that a comma-only or whitespace-only
// issue argument returns a user-facing error instead of panicking.
func TestStartCmd_emptyIssueTokens(t *testing.T) {
	tests := []struct {
		name string
		arg  string
	}{
		{"comma only", ","},
		{"spaced commas", " , "},
		{"multiple commas", ",,"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewStartCmd()
			c.SilenceUsage = true
			c.SetArgs([]string{tt.arg})
			err := c.Execute()
			if err == nil {
				t.Fatal("expected error for empty issue tokens, got nil")
			}
			if !strings.Contains(err.Error(), "no valid issue tokens found") {
				t.Errorf("wrong error message: %v", err)
			}
		})
	}
}
