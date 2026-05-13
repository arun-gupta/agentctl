package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arun-gupta/agentctl/internal/config"
)

func TestRunInfo_versionInOutput(t *testing.T) {
	var buf bytes.Buffer
	if err := runInfo(&buf, "v1.2.3", ""); err != nil {
		t.Fatalf("runInfo error: %v", err)
	}
	if !strings.Contains(buf.String(), "agentctl v1.2.3") {
		t.Errorf("output missing version line; got:\n%s", buf.String())
	}
}

func TestRunInfo_systemInOutput(t *testing.T) {
	var buf bytes.Buffer
	if err := runInfo(&buf, "dev", ""); err != nil {
		t.Fatalf("runInfo error: %v", err)
	}
	if !strings.Contains(buf.String(), "System:") {
		t.Errorf("output missing System: line; got:\n%s", buf.String())
	}
}

func TestRunInfo_agentSectionInOutput(t *testing.T) {
	var buf bytes.Buffer
	if err := runInfo(&buf, "dev", ""); err != nil {
		t.Fatalf("runInfo error: %v", err)
	}
	if !strings.Contains(buf.String(), "Coding Agents:") {
		t.Errorf("output missing Coding Agents: section; got:\n%s", buf.String())
	}
}

func TestRunInfo_noConfigWhenMissing(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := runInfo(&buf, "dev", dir); err != nil {
		t.Fatalf("runInfo error: %v", err)
	}
	if !strings.Contains(buf.String(), "Configuration: no .agentctl.yml found") {
		t.Errorf("expected no-config message; got:\n%s", buf.String())
	}
}

func TestRunInfo_withConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.AgentctlConfig{
		Editor:    "cursor",
		DevServer: "npm run dev -- --port {port}",
	}
	if err := config.Write(dir, cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var buf bytes.Buffer
	if err := runInfo(&buf, "dev", dir); err != nil {
		t.Fatalf("runInfo error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Configuration: .agentctl.yml found") {
		t.Errorf("expected config-found message; got:\n%s", out)
	}
	if !strings.Contains(out, "editor: cursor") {
		t.Errorf("expected editor field in output; got:\n%s", out)
	}
	if !strings.Contains(out, "dev_server:") {
		t.Errorf("expected dev_server field in output; got:\n%s", out)
	}
}

func TestRunInfo_noWorktreesForPlainDir(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := runInfo(&buf, "dev", dir); err != nil {
		t.Fatalf("runInfo error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Active Worktrees: 0") {
		t.Errorf("expected 'Active Worktrees: 0' for non-git dir; got:\n%s", out)
	}
}

func TestRunInfo_defaultAgentMarked(t *testing.T) {
	var buf bytes.Buffer
	if err := runInfo(&buf, "dev", ""); err != nil {
		t.Fatalf("runInfo error: %v", err)
	}
	// "claude" is the built-in default; it should be marked "(default)"
	out := buf.String()
	if !strings.Contains(out, "(default)") {
		t.Errorf("expected default agent marker in output; got:\n%s", out)
	}
}

func TestRunInfo_customDefaultAgentFromConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.AgentctlConfig{DefaultAgent: "codex"}
	if err := config.Write(dir, cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var buf bytes.Buffer
	if err := runInfo(&buf, "dev", dir); err != nil {
		t.Fatalf("runInfo error: %v", err)
	}
	out := buf.String()
	// codex should be the default now; look for "codex" with "(default)" nearby
	lines := strings.Split(out, "\n")
	found := false
	for _, line := range lines {
		if strings.Contains(line, "codex") && strings.Contains(line, "(default)") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected codex marked as default; got:\n%s", out)
	}
}

func TestRunInfo_emptyRepoRootSkipsConfigSection(t *testing.T) {
	var buf bytes.Buffer
	if err := runInfo(&buf, "dev", ""); err != nil {
		t.Fatalf("runInfo error: %v", err)
	}
	// With empty repoRoot, config and worktrees sections should be absent
	out := buf.String()
	if strings.Contains(out, "Configuration:") {
		t.Errorf("expected no Configuration: section for empty repoRoot; got:\n%s", out)
	}
}

func TestPrintConfigFields_allFields(t *testing.T) {
	cfg := &config.AgentctlConfig{
		Editor:        "cursor",
		DefaultAgent:  "codex",
		DevServer:     "npm run dev",
		MergeStrategy: "squash",
		TestCmd:       "go test ./...",
		Notify:        true,
		VCS:           config.VCSConfig{Provider: "gitlab", Server: "https://gl.example.com"},
	}

	var buf bytes.Buffer
	printConfigFields(&buf, cfg)
	out := buf.String()

	checks := []string{
		"editor: cursor",
		"default_agent: codex",
		"dev_server: npm run dev",
		"merge_strategy: squash",
		"test_cmd: go test ./...",
		"notify: true",
		"vcs.provider: gitlab",
		"vcs.server: https://gl.example.com",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("printConfigFields missing %q; got:\n%s", want, out)
		}
	}
}

func TestPrintConfigFields_emptyConfig(t *testing.T) {
	cfg := &config.AgentctlConfig{}
	var buf bytes.Buffer
	printConfigFields(&buf, cfg)
	if buf.Len() != 0 {
		t.Errorf("expected empty output for empty config; got: %q", buf.String())
	}
}

func TestParseGHVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"gh version 2.45.0 (2024-01-15)\nhttps://github.com/cli/cli/releases/tag/v2.45.0", "2.45.0"},
		{"gh version 2.10.1 (2023-06-20)", "2.10.1"},
		{"unexpected output", "unexpected output"},
		{"", ""},
	}
	for _, tt := range tests {
		got := parseGHVersion(tt.input)
		if got != tt.want {
			t.Errorf("parseGHVersion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestInfoCmd_exists(t *testing.T) {
	c := NewInfoCmd("test-version")
	if c.Use != "info" {
		t.Errorf("command Use = %q, want %q", c.Use, "info")
	}
}

func TestRunInfo_gitSection(t *testing.T) {
	var buf bytes.Buffer
	if err := runInfo(&buf, "dev", ""); err != nil {
		t.Fatalf("runInfo error: %v", err)
	}
	if !strings.Contains(buf.String(), "Git:") {
		t.Errorf("output missing Git: line; got:\n%s", buf.String())
	}
}

func TestRunInfo_ghSection(t *testing.T) {
	var buf bytes.Buffer
	if err := runInfo(&buf, "dev", ""); err != nil {
		t.Fatalf("runInfo error: %v", err)
	}
	if !strings.Contains(buf.String(), "GitHub CLI:") {
		t.Errorf("output missing GitHub CLI: line; got:\n%s", buf.String())
	}
}

func TestRunInfo_configWithDefaultAgent(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.AgentctlConfig{DefaultAgent: "gemini"}
	if err := config.Write(dir, cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var buf bytes.Buffer
	if err := runInfo(&buf, "dev", dir); err != nil {
		t.Fatalf("runInfo error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "default_agent: gemini") {
		t.Errorf("expected default_agent shown in config; got:\n%s", out)
	}
}

func TestRunInfo_installHintShown(t *testing.T) {
	// Create a temp dir with a custom adapter that won't be on PATH
	dir := t.TempDir()
	adapterDir := filepath.Join(dir, ".agentctl", "adapters")
	if err := os.MkdirAll(adapterDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	adapterYAML := "binary: nonexistent-agent-xyz-999\ninstall: npm install -g nonexistent-agent-xyz-999\n"
	if err := os.WriteFile(filepath.Join(adapterDir, "nonexistent-agent-xyz-999.yml"), []byte(adapterYAML), 0o644); err != nil {
		t.Fatalf("write adapter: %v", err)
	}

	// Change to dir so adapters.List() picks up the local adapter
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	var buf bytes.Buffer
	if err := runInfo(&buf, "dev", dir); err != nil {
		t.Fatalf("runInfo error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "install with:") {
		t.Errorf("expected install hint in output; got:\n%s", out)
	}
}
