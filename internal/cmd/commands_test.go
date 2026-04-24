package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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

func TestBuildKickoff_noSpeckit(t *testing.T) {
	kickoff := buildKickoff("42", 3010, true)
	if !contains(kickoff, "Skip the SpecKit lifecycle") {
		t.Error("no-speckit kickoff should mention skipping SpecKit")
	}
	if contains(kickoff, "STAGE 1") {
		t.Error("no-speckit kickoff should not contain STAGE 1")
	}
}

func TestBuildKickoff_speckit(t *testing.T) {
	kickoff := buildKickoff("42", 3010, false)
	if !contains(kickoff, "STAGE 1") {
		t.Error("speckit kickoff should contain STAGE 1")
	}
	if !contains(kickoff, "STAGE 2") {
		t.Error("speckit kickoff should contain STAGE 2")
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
