package cmd

import (
	"testing"
)

// TestNewAgentCmd verifies the `agent` parent command has the expected
// subcommands and description.
func TestNewAgentCmd(t *testing.T) {
	c := NewAgentCmd()

	if c.Use != "agent" {
		t.Errorf("Use = %q, want %q", c.Use, "agent")
	}
	if c.Short == "" {
		t.Error("Short description must not be empty")
	}

	want := []string{"start", "logs", "attach", "resume", "status", "diff"}
	got := map[string]bool{}
	for _, sub := range c.Commands() {
		got[sub.Name()] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("agent subcommand %q not found; have %v", name, c.Commands())
		}
	}
}

// TestNewWorktreeCmd verifies the `worktree` parent command has the expected
// subcommands and description.
func TestNewWorktreeCmd(t *testing.T) {
	c := NewWorktreeCmd()

	if c.Use != "worktree" {
		t.Errorf("Use = %q, want %q", c.Use, "worktree")
	}
	if c.Short == "" {
		t.Error("Short description must not be empty")
	}

	want := []string{"cleanup", "discard", "merge", "open"}
	got := map[string]bool{}
	for _, sub := range c.Commands() {
		got[sub.Name()] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("worktree subcommand %q not found; have %v", name, c.Commands())
		}
	}
}

// TestRootCmdHierarchy verifies that the agent and worktree parent commands
// contain the expected subcommands by constructing them directly (independent
// of how the root wires them up — that is covered by TestRootCmdRegistration
// in cmd/agentctl/main_test.go).
func TestRootCmdHierarchy(t *testing.T) {
	agentCmd := NewAgentCmd()
	worktreeCmd := NewWorktreeCmd()

	agentSubs := map[string]bool{}
	for _, sub := range agentCmd.Commands() {
		agentSubs[sub.Name()] = true
	}
	worktreeSubs := map[string]bool{}
	for _, sub := range worktreeCmd.Commands() {
		worktreeSubs[sub.Name()] = true
	}

	for _, name := range []string{"start", "logs", "attach", "resume", "status", "diff"} {
		if !agentSubs[name] {
			t.Errorf("expected `agent %s` subcommand to exist", name)
		}
	}
	for _, name := range []string{"cleanup", "discard", "merge", "open"} {
		if !worktreeSubs[name] {
			t.Errorf("expected `worktree %s` subcommand to exist", name)
		}
	}
}
