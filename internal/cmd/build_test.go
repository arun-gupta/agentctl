package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arun-gupta/agentctl/internal/config"
	"github.com/arun-gupta/agentctl/internal/git"
)

// initBuildRepo creates a temporary git repo with an initial commit.
// Tests are skipped when git is not available.
func initBuildRepo(t *testing.T) string {
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
	gitRun("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", ".")
	gitRun("commit", "-m", "init")
	return dir
}

// ─── NewBuildCmd flags ────────────────────────────────────────────────────────

func TestNewBuildCmd_flags(t *testing.T) {
	c := NewBuildCmd()
	if c.Use != "build <issue>" {
		t.Errorf("Use = %q, want %q", c.Use, "build <issue>")
	}

	outputFlag := c.Flags().Lookup("output")
	if outputFlag == nil {
		t.Fatal("--output flag not registered")
	}
	if outputFlag.Shorthand != "o" {
		t.Errorf("--output shorthand = %q, want %q", outputFlag.Shorthand, "o")
	}

	agentFlag := c.Flags().Lookup("agent")
	if agentFlag == nil {
		t.Fatal("--agent flag not registered")
	}
}

// ─── detectBuildCmd ───────────────────────────────────────────────────────────

func TestDetectBuildCmd_goMod_withCmdDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdDir := filepath.Join(dir, "cmd", "myapp")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\nfunc main(){}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := detectBuildCmd(dir, "/tmp/out")
	if err != nil {
		t.Fatalf("detectBuildCmd: %v", err)
	}
	if !strings.Contains(got, "go build") {
		t.Errorf("expected go build in cmd, got %q", got)
	}
	if !strings.Contains(got, "/tmp/out") {
		t.Errorf("expected output path in cmd, got %q", got)
	}
	if !strings.Contains(got, "cmd/myapp") {
		t.Errorf("expected cmd/myapp in cmd, got %q", got)
	}
}

func TestDetectBuildCmd_goMod_noCmdDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := detectBuildCmd(dir, "/tmp/out")
	if err != nil {
		t.Fatalf("detectBuildCmd: %v", err)
	}
	if !strings.Contains(got, "go build") {
		t.Errorf("expected go build in cmd, got %q", got)
	}
	if !strings.Contains(got, "/tmp/out") {
		t.Errorf("expected output path in cmd, got %q", got)
	}
	if !strings.Contains(got, "./") {
		t.Errorf("expected module root ./  in cmd, got %q", got)
	}
}

func TestDetectBuildCmd_packageJSON_withBuildScript(t *testing.T) {
	dir := t.TempDir()
	pkgJSON := `{"name":"foo","scripts":{"build":"webpack"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := detectBuildCmd(dir, "/tmp/out")
	if err != nil {
		t.Fatalf("detectBuildCmd: %v", err)
	}
	if got != "npm run build" {
		t.Errorf("detectBuildCmd = %q, want %q", got, "npm run build")
	}
}

func TestDetectBuildCmd_packageJSON_noBuildScript(t *testing.T) {
	dir := t.TempDir()
	pkgJSON := `{"name":"foo","scripts":{"test":"jest"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := detectBuildCmd(dir, "/tmp/out")
	if err == nil {
		t.Error("expected error for package.json without build script, got nil")
	}
}

func TestDetectBuildCmd_makefile_withBuildTarget(t *testing.T) {
	dir := t.TempDir()
	makefile := "build:\n\tgo build -o bin/app .\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := detectBuildCmd(dir, "/tmp/out")
	if err != nil {
		t.Fatalf("detectBuildCmd: %v", err)
	}
	if got != "make build" {
		t.Errorf("detectBuildCmd = %q, want %q", got, "make build")
	}
}

func TestDetectBuildCmd_makefile_noBuildTarget(t *testing.T) {
	dir := t.TempDir()
	makefile := "test:\n\tgo test ./...\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := detectBuildCmd(dir, "/tmp/out")
	if err == nil {
		t.Error("expected error for Makefile without build target, got nil")
	}
}

func TestDetectBuildCmd_noMatchingFiles(t *testing.T) {
	dir := t.TempDir()

	_, err := detectBuildCmd(dir, "/tmp/out")
	if err == nil {
		t.Error("expected error when no build files found, got nil")
	}
}

// ─── resolveBuildCmd ──────────────────────────────────────────────────────────

func TestResolveBuildCmd_usesConfigWhenSet(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveBuildCmd(dir, "go build -o {output} ./cmd/myapp", "/tmp/mybin")
	if err != nil {
		t.Fatalf("resolveBuildCmd: %v", err)
	}
	want := "go build -o /tmp/mybin ./cmd/myapp"
	if got != want {
		t.Errorf("resolveBuildCmd = %q, want %q", got, want)
	}
}

func TestResolveBuildCmd_fallsBackToDetect(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveBuildCmd(dir, "", "/tmp/out")
	if err != nil {
		t.Fatalf("resolveBuildCmd: %v", err)
	}
	if !strings.Contains(got, "go build") {
		t.Errorf("expected go build, got %q", got)
	}
}

// ─── runBuild integration ─────────────────────────────────────────────────────

func TestRunBuild_noWorktreeNoBranch(t *testing.T) {
	repo := initBuildRepo(t)
	outPath := filepath.Join(t.TempDir(), "out")

	// runBuild needs git.RepoRoot() to work; run it from within the repo.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	err = runBuild("999", outPath, "", &strings.Builder{})
	if err == nil {
		t.Fatal("expected error when no worktree and no branch exist, got nil")
	}
	if !strings.Contains(err.Error(), "no worktree") && !strings.Contains(err.Error(), "no branch") {
		t.Errorf("error message %q should mention worktree or branch", err.Error())
	}
}

func TestRunBuild_worktreeExists_usesConfigBuildCmd(t *testing.T) {
	repo := initBuildRepo(t)

	// Create a linked worktree for issue 42.
	wtPath := filepath.Join(t.TempDir(), "wt-42-feature")
	if err := git.AddWorktree(repo, wtPath, "42-feature"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	// Write a .agentctl.yml into the worktree with a build_cmd that touches a file.
	outPath := filepath.Join(t.TempDir(), "mybin")
	touchCmd := "touch " + outPath
	cfg := &config.AgentctlConfig{BuildCmd: touchCmd}
	if err := config.Write(repo, cfg); err != nil {
		t.Fatalf("config.Write: %v", err)
	}

	// Change into repo so git.RepoRoot() resolves.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runBuild("42", outPath, "", &out); err != nil {
		t.Fatalf("runBuild: %v", err)
	}
	if !strings.Contains(out.String(), "Built:") {
		t.Errorf("output %q should contain 'Built:'", out.String())
	}
	if !strings.Contains(out.String(), outPath) {
		t.Errorf("output %q should contain output path", out.String())
	}
}

func TestRunBuild_branchExists_checksOutTempWorktree(t *testing.T) {
	repo := initBuildRepo(t)

	// Create a local branch for issue 55 (no linked worktree).
	branchCmd := exec.Command("git", "-C", repo, "branch", "55-my-feature")
	if out, err := branchCmd.CombinedOutput(); err != nil {
		t.Fatalf("create branch: %v\n%s", err, out)
	}

	// Write a .agentctl.yml with a build_cmd that touches a file.
	outPath := filepath.Join(t.TempDir(), "built-bin")
	touchCmd := "touch " + outPath
	cfg := &config.AgentctlConfig{BuildCmd: touchCmd}
	if err := config.Write(repo, cfg); err != nil {
		t.Fatalf("config.Write: %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runBuild("55", outPath, "", &out); err != nil {
		t.Fatalf("runBuild: %v", err)
	}
	if !strings.Contains(out.String(), "Built:") {
		t.Errorf("output %q should contain 'Built:'", out.String())
	}
}

func TestRunBuild_defaultOutputPath(t *testing.T) {
	repo := initBuildRepo(t)

	// Create a linked worktree for issue 77.
	wtPath := filepath.Join(t.TempDir(), "wt-77-thing")
	if err := git.AddWorktree(repo, wtPath, "77-thing"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	// Use a build_cmd that echoes the output path back.
	cfg := &config.AgentctlConfig{BuildCmd: "echo built {output}"}
	if err := config.Write(repo, cfg); err != nil {
		t.Fatalf("config.Write: %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	// Pass empty output path to trigger default.
	if err := runBuild("77", "", "", &out); err != nil {
		t.Fatalf("runBuild: %v", err)
	}
	output := out.String()
	if !strings.Contains(output, "Built:") {
		t.Errorf("output %q should contain 'Built:'", output)
	}
	// Default path should contain the issue number.
	if !strings.Contains(output, "77") {
		t.Errorf("default output path in %q should contain issue number 77", output)
	}
}
