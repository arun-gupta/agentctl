package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arun-gupta/agentctl/internal/sdd"
	"github.com/arun-gupta/agentctl/internal/state"
)

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
	state := computeSpecState(dir, "42")
	if state != "no-spec" {
		t.Errorf("expected no-spec, got %q", state)
	}
}

func TestComputeSpecState_emptyIssue(t *testing.T) {
	dir := t.TempDir()
	state := computeSpecState(dir, "")
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
	state := computeSpecState(dir, "42")
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
	state := computeSpecState(dir, "42")
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
	state := computeSpecState(dir, "42")
	if state != "done" {
		t.Errorf("expected done, got %q", state)
	}
}

func TestSpecExists_absent(t *testing.T) {
	dir := t.TempDir()
	if specExists(dir) {
		t.Error("expected specExists to return false for empty dir")
	}
}

func TestSpecExists_present(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "42-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "spec.md"), []byte("spec"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !specExists(dir) {
		t.Error("expected specExists to return true when spec.md exists")
	}
}

func TestSkipPrompt_noSDDFlag(t *testing.T) {
	kickoff := sdd.SkipPrompt("42", "3010")
	if !contains(kickoff, "Skip the SDD lifecycle") {
		t.Error("no-sdd kickoff should mention skipping SDD")
	}
	if contains(kickoff, "/speckit") {
		t.Error("no-sdd kickoff should not contain speckit-specific commands")
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
		"https://github.com/myorg/myrepo/pull/42",   // pull request URL
		"https://github.com/myorg/myrepo/issues/",   // missing number
		"https://github.com/myorg/myrepo/issues/abc", // non-numeric
		"https://github.com/myorg/myrepo",            // no issues path
		"https://example.com/owner/repo/issues/1",   // wrong host
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

	got, err := locateOrCloneRepo("myorg", "myrepo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dir {
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

	got, err := locateOrCloneRepo("myorg", "myrepo")
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

	_, err := locateOrCloneRepo("myorg", "myrepo")
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

	root, issueNum, ghArg, err := repoRootForIssue("42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root != dir {
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
	root, issueNum, ghArg, err := repoRootForIssue(rawURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root != dir {
		t.Errorf("root = %q, want %q", root, dir)
	}
	if issueNum != "99" {
		t.Errorf("issueNum = %q, want %q", issueNum, "99")
	}
	if ghArg != rawURL {
		t.Errorf("ghArg = %q, want %q", ghArg, rawURL)
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
	err := launchAgent("nonexistent-xyz-abc", dir, "42", "3010", "sess-123", "kickoff", true, false)
	if err == nil {
		t.Error("expected error for unknown adapter")
	}
}

func TestLaunchAgent_binaryNotFound(t *testing.T) {
	dir := t.TempDir()
	writeLocalAdapter(t, dir, "fakebinary", "binary: __nonexistent_binary_xyz__\n")
	chdirTemp(t, dir)

	err := launchAgent("fakebinary", dir, "42", "3010", "sess-123", "kickoff", true, false)
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

	err := launchAgent("echoagent", dir, "42", "3010", "sess-abc", "do the thing", true, false)
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
		done <- launchAgent("echoagent", dir, "42", "3010", "sess-abc", "do the thing", false, false)
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
		done <- launchAgent("claude", dir, "42", "3010", "sess-abc", "kickoff text", false, false)
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

// TestLaunchAgent_claudeHeadlessInjectsVerboseOnly verifies that launchAgent
// appends only --verbose (not --output-format stream-json) to the claude
// command line in headless mode.
func TestLaunchAgent_claudeHeadlessInjectsVerboseOnly(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "argv.txt")

	scriptPath := filepath.Join(dir, "claude-stub")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + argsFile + "\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	writeLocalAdapter(t, dir, "claude", "binary: "+scriptPath+"\nsession: --session\n")
	chdirTemp(t, dir)

	if err := launchAgent("claude", dir, "42", "3010", "sess-abc", "kickoff text", true, false); err != nil {
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
	if !strings.Contains(argsStr, "--verbose") {
		t.Errorf("missing --verbose in headless claude argv: %q", argsStr)
	}
	if strings.Contains(argsStr, "--output-format") {
		t.Errorf("unexpected --output-format in headless claude argv: %q", argsStr)
	}
	if strings.Contains(argsStr, "stream-json") {
		t.Errorf("unexpected stream-json in headless claude argv: %q", argsStr)
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
	launchErr := launchAgent("falseagent", dir, "42", "3010", "sess-abc", "do the thing", false, false)

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
	err := agentResume("nonexistent-xyz-abc", dir, "sess-123", "my feedback")
	if err == nil {
		t.Error("expected error for unknown adapter")
	}
}

func TestAgentResume_success(t *testing.T) {
	dir := t.TempDir()
	// Use `echo` as the resume binary — always on PATH, exits immediately.
	writeLocalAdapter(t, dir, "echoagent",
		"binary: echo\nsession: --session\n")
	chdirTemp(t, dir)

	if err := agentResume("echoagent", dir, "sess-123", "my feedback"); err != nil {
		t.Errorf("agentResume: %v", err)
	}
}

// ─── streamLog ────────────────────────────────────────────────────────────────

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
		followLog(logPath, &buf, done, false)
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
		followLog(logPath, &buf, done, false)
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
		followLog(logPath, &buf, done, true)
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
		Agent:    "claude",
		SessionID: "s1",
		DevPID:   "0",
		AgentPID: strconv.Itoa(pid),
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
		Agent:    "claude",
		SessionID: "s1",
		DevPID:   "0",
		AgentPID: strconv.Itoa(pid),
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

func TestExtractStreamText(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		want  string
	}{
		{
			name:  "assistant text",
			line:  `{"type":"assistant","message":{"content":[{"type":"text","text":"I'll fix the bug."}]}}`,
			want:  "I'll fix the bug.",
		},
		{
			name:  "assistant tool_use",
			line:  `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"bash","input":{"command":"ls"}}]}}`,
			want:  "[bash]",
		},
		{
			name:  "assistant text + tool_use",
			line:  `{"type":"assistant","message":{"content":[{"type":"text","text":"Running ls."},{"type":"tool_use","name":"bash","input":{}}]}}`,
			want:  "Running ls.\n[bash]",
		},
		{
			name:  "result success",
			line:  `{"type":"result","subtype":"success","result":"PR opened."}`,
			want:  "PR opened.",
		},
		{
			name:  "system event skipped",
			line:  `{"type":"system","subtype":"init","model":"claude-opus-4-5"}`,
			want:  "",
		},
		{
			name:  "user event skipped",
			line:  `{"type":"user","message":{"role":"user","content":[]}}`,
			want:  "",
		},
		{
			name:  "non-JSON passed through",
			line:  "plain text output",
			want:  "plain text output",
		},
		{
			name:  "empty assistant text skipped",
			line:  `{"type":"assistant","message":{"content":[{"type":"text","text":"   "}]}}`,
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractStreamText(tc.line)
			if got != tc.want {
				t.Errorf("extractStreamText(%q) = %q, want %q", tc.line, got, tc.want)
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
