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

func TestSkipPrompt_noSDD(t *testing.T) {
	kickoff := sdd.SkipPrompt("42", "3010")
	if !contains(kickoff, "Skip the SDD lifecycle") {
		t.Error("no-sdd kickoff should mention skipping SDD")
	}
	if contains(kickoff, "/speckit") {
		t.Error("no-sdd kickoff should not contain speckit-specific commands")
	}
}

func TestStartCmd_noSpeckitFlagRemoved(t *testing.T) {
	c := NewStartCmd()
	if f := c.Flags().Lookup("no-speckit"); f != nil {
		t.Error("--no-speckit flag must not be registered; it was removed")
	}
}

func TestStartCmd_sddFlagExists(t *testing.T) {
	c := NewStartCmd()
	f := c.Flags().Lookup("sdd")
	if f == nil {
		t.Fatal("--sdd flag must be registered")
	}
	if f.DefValue != "speckit" {
		t.Errorf("--sdd default should be 'speckit', got %q", f.DefValue)
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

// contains is a simple substring helper for tests.
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
	err := launchAgent("nonexistent-xyz-abc", dir, "42", "3010", "sess-123", "kickoff", true)
	if err == nil {
		t.Error("expected error for unknown adapter")
	}
}

func TestLaunchAgent_binaryNotFound(t *testing.T) {
	dir := t.TempDir()
	writeLocalAdapter(t, dir, "fakebinary", "binary: __nonexistent_binary_xyz__\n")
	chdirTemp(t, dir)

	err := launchAgent("fakebinary", dir, "42", "3010", "sess-123", "kickoff", true)
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

	err := launchAgent("echoagent", dir, "42", "3010", "sess-abc", "do the thing", true)
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

// ─── agentResume ─────────────────────────────────────────────────────────────

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
