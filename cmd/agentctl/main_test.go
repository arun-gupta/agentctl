package main

import "testing"

// TestRootCmdSilenceErrors verifies that the root command suppresses cobra's
// built-in error printing so errors are not echoed twice (once by cobra, once
// by main's fmt.Fprintln).
func TestRootCmdSilenceErrors(t *testing.T) {
	root := newRootCmd()
	if !root.SilenceErrors {
		t.Error("root command must set SilenceErrors: true to prevent cobra from printing errors a second time after main already prints them")
	}
}

func TestRootCmd_registersInfoCommandOnce(t *testing.T) {
	root := newRootCmd()
	infoCount := 0
	for _, c := range root.Commands() {
		if c.Name() == "info" {
			infoCount++
		}
	}
	if infoCount != 1 {
		t.Fatalf("expected exactly one info command registration, got %d", infoCount)
	}
}

// TestRootCmdRegistration verifies that the root command has both the new
// parent commands and the hidden backward-compat `start` alias registered.
func TestRootCmdRegistration(t *testing.T) {
	root := newRootCmd()

	found := map[string]bool{}
	hidden := map[string]bool{}
	for _, c := range root.Commands() {
		found[c.Name()] = true
		hidden[c.Name()] = c.Hidden
	}

	// Both two-level parent commands must be present.
	for _, name := range []string{"agent", "worktree"} {
		if !found[name] {
			t.Errorf("expected top-level command %q to be registered", name)
		}
	}

	// Hidden backward-compat alias must be present at root level.
	if !found["start"] {
		t.Fatal("expected hidden `start` alias to be registered at root level")
	}
	if !hidden["start"] {
		t.Error("`start` alias at root level must be hidden")
	}
}
