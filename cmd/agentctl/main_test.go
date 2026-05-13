package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	// Two-level parent commands must be present.
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

// TestSetupCmd_e2e verifies that "agentctl setup" is wired correctly end-to-end:
// the command is reachable via the root, accepts stdin, writes the global config
// file to the real path under XDG_CONFIG_HOME, and exits without error.
func TestSetupCmd_e2e(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	root := newRootCmd()
	// Pipe empty input so all prompts fall through to defaults.
	root.SetIn(strings.NewReader("\n\n\n"))

	root.SetArgs([]string{"setup"})
	if err := root.Execute(); err != nil {
		t.Fatalf("agentctl setup: %v", err)
	}

	cfgPath := filepath.Join(tmp, "agentctl", "config.yml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("global config not written at %s: %v", cfgPath, err)
	}
	content := string(data)
	if !strings.Contains(content, "default_agent") {
		t.Errorf("config missing default_agent field:\n%s", content)
	}
	if !strings.Contains(content, "notify") {
		t.Errorf("config missing notify field:\n%s", content)
	}
}
