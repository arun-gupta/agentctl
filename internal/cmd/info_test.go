package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arun-gupta/agentctl/internal/config"
	"github.com/arun-gupta/agentctl/internal/state"
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
	dir := t.TempDir()
	adapterDir := filepath.Join(dir, ".agentctl", "adapters")
	if err := os.MkdirAll(adapterDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	adapterYAML := "binary: nonexistent-agent-xyz-999\ninstall: npm install -g nonexistent-agent-xyz-999\n"
	if err := os.WriteFile(filepath.Join(adapterDir, "nonexistent-agent-xyz-999.yml"), []byte(adapterYAML), 0o644); err != nil {
		t.Fatalf("write adapter: %v", err)
	}

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

// ─── worktree filtering ───────────────────────────────────────────────────────

func initGitRepoForInfo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := t.TempDir()
	gitRunInfo := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitRunInfo("init")
	gitRunInfo("config", "user.email", "test@example.com")
	gitRunInfo("config", "user.name", "Test")
	gitRunInfo("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRunInfo("add", ".")
	gitRunInfo("commit", "-m", "init")
	return dir
}

func TestRunInfo_skipsWorktreesWithoutAgentFile(t *testing.T) {
	repo := initGitRepoForInfo(t)
	wtPath := filepath.Join(t.TempDir(), "42-no-agent")
	cmd := exec.Command("git", "-C", repo, "worktree", "add", wtPath, "-b", "42-no-agent")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	// No .agent file written — simulates a manually-created worktree.

	var buf bytes.Buffer
	if err := runInfo(&buf, "dev", repo); err != nil {
		t.Fatalf("runInfo: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "42-no-agent") {
		t.Errorf("worktree without .agent file should be skipped in info output; got:\n%s", out)
	}
	if !strings.Contains(out, "Active Worktrees: 0") {
		t.Errorf("expected 'Active Worktrees: 0' when all worktrees lack .agent; got:\n%s", out)
	}
}

func TestRunInfo_showsWorktreesWithAgentFile(t *testing.T) {
	repo := initGitRepoForInfo(t)
	wtPath := filepath.Join(t.TempDir(), "99-with-agent")
	cmd := exec.Command("git", "-C", repo, "worktree", "add", wtPath, "-b", "99-with-agent")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	if err := state.Write(wtPath, state.AgentFile{Agent: "claude"}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runInfo(&buf, "dev", repo); err != nil {
		t.Fatalf("runInfo: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "99-with-agent") {
		t.Errorf("worktree with .agent file should appear in info output; got:\n%s", out)
	}
	if !strings.Contains(out, "Active Worktrees: 1") {
		t.Errorf("expected 'Active Worktrees: 1'; got:\n%s", out)
	}
}

func TestRunInfo_mixedWorktrees_onlyManagedShown(t *testing.T) {
	repo := initGitRepoForInfo(t)

	// Manual worktree without .agent file
	manualPath := filepath.Join(t.TempDir(), "manual-review")
	cmd := exec.Command("git", "-C", repo, "worktree", "add", manualPath, "-b", "manual-review")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add (manual): %v\n%s", err, out)
	}

	// Agentctl-managed worktree with .agent file
	managedPath := filepath.Join(t.TempDir(), "55-my-feature")
	cmd = exec.Command("git", "-C", repo, "worktree", "add", managedPath, "-b", "55-my-feature")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add (managed): %v\n%s", err, out)
	}
	if err := state.Write(managedPath, state.AgentFile{Agent: "claude"}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runInfo(&buf, "dev", repo); err != nil {
		t.Fatalf("runInfo: %v", err)
	}
	out := buf.String()

	if strings.Contains(out, "manual-review") {
		t.Errorf("manual worktree (no .agent) should be skipped; got:\n%s", out)
	}
	if !strings.Contains(out, "55-my-feature") {
		t.Errorf("managed worktree should appear; got:\n%s", out)
	}
	if !strings.Contains(out, "Active Worktrees: 1") {
		t.Errorf("expected 'Active Worktrees: 1' (only managed ones counted); got:\n%s", out)
	}
}
