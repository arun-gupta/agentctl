package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arun-gupta/agentctl/internal/config"
)

// stubWriteGlobal temporarily replaces writeGlobalFn for the duration of the test.
func stubWriteGlobal(t *testing.T, fn func(*config.GlobalConfig) error) {
	t.Helper()
	orig := writeGlobalFn
	writeGlobalFn = fn
	t.Cleanup(func() { writeGlobalFn = orig })
}

func TestSetupCmd_exists(t *testing.T) {
	c := NewSetupCmd()
	if c.Use != "setup" {
		t.Errorf("command Use = %q, want %q", c.Use, "setup")
	}
}

func TestSetupCmd_shortDescription(t *testing.T) {
	c := NewSetupCmd()
	if c.Short == "" {
		t.Error("command Short must not be empty")
	}
}

func TestRunSetup_welcomeMessage(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	stubWriteGlobal(t, func(*config.GlobalConfig) error { return nil })

	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if !strings.Contains(buf.String(), "Welcome to agentctl setup") {
		t.Errorf("expected welcome message; got:\n%s", buf.String())
	}
}

func TestRunSetup_prerequisitesSection(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	stubWriteGlobal(t, func(*config.GlobalConfig) error { return nil })

	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if !strings.Contains(buf.String(), "Checking prerequisites") {
		t.Errorf("expected prerequisites section; got:\n%s", buf.String())
	}
}

func TestRunSetup_agentsSection(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	stubWriteGlobal(t, func(*config.GlobalConfig) error { return nil })

	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if !strings.Contains(buf.String(), "Coding Agents") {
		t.Errorf("expected coding agents section; got:\n%s", buf.String())
	}
}

func TestRunSetup_notificationsPrompt(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	stubWriteGlobal(t, func(*config.GlobalConfig) error { return nil })

	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if !strings.Contains(buf.String(), "desktop notifications") {
		t.Errorf("expected notifications prompt; got:\n%s", buf.String())
	}
}

func TestRunSetup_sddPrompt(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	stubWriteGlobal(t, func(*config.GlobalConfig) error { return nil })

	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if !strings.Contains(buf.String(), "SDD methodology") {
		t.Errorf("expected SDD methodology prompt; got:\n%s", buf.String())
	}
}

func TestRunSetup_configSavedMessage(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	stubWriteGlobal(t, func(*config.GlobalConfig) error { return nil })

	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if !strings.Contains(buf.String(), "Configuration saved to") {
		t.Errorf("expected config-saved message; got:\n%s", buf.String())
	}
}

func TestRunSetup_nextStepsShown(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	stubWriteGlobal(t, func(*config.GlobalConfig) error { return nil })

	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if !strings.Contains(buf.String(), "Next steps") {
		t.Errorf("expected next steps section; got:\n%s", buf.String())
	}
}

func TestRunSetup_emptyInputUsesDefaultAgent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	var captured *config.GlobalConfig
	stubWriteGlobal(t, func(cfg *config.GlobalConfig) error {
		captured = cfg
		return nil
	})

	var buf bytes.Buffer
	// Empty input — all prompts fall through to defaults.
	if err := runSetup(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected WriteGlobal to be called")
	}
	// Default agent is "claude" when nothing is set.
	if captured.DefaultAgent != "claude" {
		t.Errorf("default agent = %q, want %q", captured.DefaultAgent, "claude")
	}
}

func TestRunSetup_emptyInputEnablesNotifyByDefault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	var captured *config.GlobalConfig
	stubWriteGlobal(t, func(cfg *config.GlobalConfig) error {
		captured = cfg
		return nil
	})

	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected WriteGlobal to be called")
	}
	if captured.Notify == nil || !*captured.Notify {
		t.Errorf("expected notify=true by default; got %v", captured.Notify)
	}
}

func TestRunSetup_selectDisableNotifications(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	var captured *config.GlobalConfig
	stubWriteGlobal(t, func(cfg *config.GlobalConfig) error {
		captured = cfg
		return nil
	})

	// Input: skip agent prompt (empty=default), select "2" (No) for notifications, skip SDD prompt.
	input := "\n2\n\n"
	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(input), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected WriteGlobal to be called")
	}
	if captured.Notify == nil || *captured.Notify {
		t.Errorf("expected notify=false when user selects No; got %v", captured.Notify)
	}
}

func TestRunSetup_selectAgentByNumber(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	var captured *config.GlobalConfig
	stubWriteGlobal(t, func(cfg *config.GlobalConfig) error {
		captured = cfg
		return nil
	})

	// Input "2" to select the second agent in the list, then defaults for the rest.
	input := "2\n\n\n"
	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(input), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected WriteGlobal to be called")
	}
	// Second agent in List() (built-in order). Just verify it's not empty.
	if captured.DefaultAgent == "" {
		t.Error("expected a non-empty DefaultAgent after selecting agent 2")
	}
}

func TestRunSetup_selectSddByNumber(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	var captured *config.GlobalConfig
	stubWriteGlobal(t, func(cfg *config.GlobalConfig) error {
		captured = cfg
		return nil
	})

	// Input: default agent, default notifications, select "2" for SDD.
	input := "\n\n2\n"
	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(input), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected WriteGlobal to be called")
	}
	if captured.DefaultSDD == "" {
		t.Error("expected non-empty DefaultSDD when selecting option 2")
	}
}

func TestRunSetup_invalidAgentInputFallsToDefault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	var captured *config.GlobalConfig
	stubWriteGlobal(t, func(cfg *config.GlobalConfig) error {
		captured = cfg
		return nil
	})

	// "999" is out of range — should fall through to the current default.
	input := "999\n\n\n"
	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(input), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected WriteGlobal to be called")
	}
	if captured.DefaultAgent != "claude" {
		t.Errorf("out-of-range input should keep default 'claude'; got %q", captured.DefaultAgent)
	}
}

func TestRunSetup_writeErrorReturned(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	stubWriteGlobal(t, func(*config.GlobalConfig) error {
		return os.ErrPermission
	})

	var buf bytes.Buffer
	err := runSetup(strings.NewReader(""), &buf)
	if err == nil {
		t.Error("expected error when WriteGlobal fails; got nil")
	}
}

func TestRunSetup_prereqGitInstalled(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	stubWriteGlobal(t, func(*config.GlobalConfig) error { return nil })
	stubToolRunner(t, func(name string, args ...string) (string, error) {
		if name == "git" {
			return "git version 2.39.2", nil
		}
		return "", os.ErrNotExist
	})

	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if !strings.Contains(buf.String(), "2.39.2") {
		t.Errorf("expected git version in output; got:\n%s", buf.String())
	}
}

func TestRunSetup_prereqGhMissing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	stubWriteGlobal(t, func(*config.GlobalConfig) error { return nil })
	stubLookPath(t, func(name string) (string, error) {
		// gh not found
		return "", os.ErrNotExist
	})

	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "GitHub CLI") || !strings.Contains(out, "not found") {
		t.Errorf("expected 'GitHub CLI not found' message; got:\n%s", out)
	}
}

func TestRunSetup_prereqGhInNextSteps(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	stubWriteGlobal(t, func(*config.GlobalConfig) error { return nil })
	stubLookPath(t, func(name string) (string, error) {
		return "", os.ErrNotExist
	})

	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	out := buf.String()
	// Missing gh should appear in next steps.
	nextStepsIdx := strings.Index(out, "Next steps")
	if nextStepsIdx < 0 {
		t.Fatalf("expected 'Next steps' in output; got:\n%s", out)
	}
	nextSteps := out[nextStepsIdx:]
	if !strings.Contains(nextSteps, "gh") {
		t.Errorf("expected gh install hint in next steps; got:\n%s", nextSteps)
	}
}

func TestRunSetup_existingGlobalConfigPreserved(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	// Write an existing global config with editor set.
	globalDir := filepath.Join(tmp, "agentctl")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.yml"),
		[]byte("editor: cursor\nmerge_strategy: squash\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var captured *config.GlobalConfig
	stubWriteGlobal(t, func(cfg *config.GlobalConfig) error {
		captured = cfg
		return nil
	})

	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	if captured == nil {
		t.Fatal("expected WriteGlobal to be called")
	}
	// Editor and merge strategy should be preserved from existing config.
	if captured.Editor != "cursor" {
		t.Errorf("expected editor='cursor' to be preserved; got %q", captured.Editor)
	}
	if captured.MergeStrategy != "squash" {
		t.Errorf("expected merge_strategy='squash' to be preserved; got %q", captured.MergeStrategy)
	}
}

func TestRunSetup_agentctlInfoInNextSteps(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	stubWriteGlobal(t, func(*config.GlobalConfig) error { return nil })

	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	out := buf.String()
	nextStepsIdx := strings.Index(out, "Next steps")
	if nextStepsIdx < 0 {
		t.Fatalf("expected 'Next steps' in output; got:\n%s", out)
	}
	nextSteps := out[nextStepsIdx:]
	if !strings.Contains(nextSteps, "agentctl info") {
		t.Errorf("expected 'agentctl info' in next steps; got:\n%s", nextSteps)
	}
}

func TestRunSetup_agentctlStartInNextSteps(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	stubWriteGlobal(t, func(*config.GlobalConfig) error { return nil })

	var buf bytes.Buffer
	if err := runSetup(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("runSetup error: %v", err)
	}
	out := buf.String()
	nextStepsIdx := strings.Index(out, "Next steps")
	if nextStepsIdx < 0 {
		t.Fatalf("expected 'Next steps' in output; got:\n%s", out)
	}
	nextSteps := out[nextStepsIdx:]
	if !strings.Contains(nextSteps, "agentctl start") {
		t.Errorf("expected 'agentctl start' in next steps; got:\n%s", nextSteps)
	}
}
