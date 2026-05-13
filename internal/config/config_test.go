package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arun-gupta/agentctl/internal/config"
)

func boolPtr(b bool) *bool { return &b }

func TestRead_missingFile_returnsEmpty(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Read(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DevServer != "" || cfg.Notify != nil {
		t.Errorf("expected empty config for missing file, got %+v", cfg)
	}
}

func TestRead_parsesDevServer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".agentctl.yml"), []byte("dev_server: \"uvicorn main:app --port {port}\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Read(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DevServer != "uvicorn main:app --port {port}" {
		t.Errorf("DevServer = %q, want %q", cfg.DevServer, "uvicorn main:app --port {port}")
	}
}

func TestWrite_preservesDevServer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".agentctl.yml"), []byte("dev_server: \"uvicorn main:app --port {port}\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Write(dir, cfg); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	got, err := config.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.DevServer != "uvicorn main:app --port {port}" {
		t.Errorf("DevServer lost after Write: %q", got.DevServer)
	}
}

func TestWrite_createsFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.AgentctlConfig{DevServer: "npm start"}
	if err := config.Write(dir, cfg); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	got, err := config.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.DevServer != "npm start" {
		t.Errorf("DevServer = %q, want %q", got.DevServer, "npm start")
	}
}

func TestRead_parsesNotify(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".agentctl.yml"), []byte("notify: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Read(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Notify == nil || !*cfg.Notify {
		t.Errorf("Notify = %v, want true", cfg.Notify)
	}
}

func TestRead_notifyDefaultsFalse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".agentctl.yml"), []byte("dev_server: \"uvicorn main:app --port {port}\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Read(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Notify != nil {
		t.Errorf("Notify = %v, want nil (not set)", *cfg.Notify)
	}
}

func TestRead_parsesMergeStrategy(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".agentctl.yml"), []byte("merge_strategy: rebase\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Read(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MergeStrategy != "rebase" {
		t.Errorf("MergeStrategy = %q, want %q", cfg.MergeStrategy, "rebase")
	}
}

func TestRead_mergeStrategyDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".agentctl.yml"), []byte("dev_server: \"npm start\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Read(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MergeStrategy != "" {
		t.Errorf("MergeStrategy = %q, want empty string (default)", cfg.MergeStrategy)
	}
}

func TestWrite_preservesMergeStrategy(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.AgentctlConfig{MergeStrategy: "squash"}
	if err := config.Write(dir, cfg); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	got, err := config.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.MergeStrategy != "squash" {
		t.Errorf("MergeStrategy = %q, want %q", got.MergeStrategy, "squash")
	}
}

func TestRead_parsesTestCmd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".agentctl.yml"), []byte("test_cmd: \"go test ./...\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Read(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TestCmd != "go test ./..." {
		t.Errorf("TestCmd = %q, want %q", cfg.TestCmd, "go test ./...")
	}
}

func TestRead_testCmdDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".agentctl.yml"), []byte("dev_server: \"npm start\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Read(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TestCmd != "" {
		t.Errorf("TestCmd = %q, want empty string (default)", cfg.TestCmd)
	}
}

func TestWrite_preservesTestCmd(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.AgentctlConfig{TestCmd: "pytest"}
	if err := config.Write(dir, cfg); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	got, err := config.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.TestCmd != "pytest" {
		t.Errorf("TestCmd = %q, want %q", got.TestCmd, "pytest")
	}
}

func TestRead_parsesVCSProvider(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".agentctl.yml"), []byte("vcs:\n  provider: gitlab\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Read(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.VCS.Provider != "gitlab" {
		t.Errorf("VCS.Provider = %q, want %q", cfg.VCS.Provider, "gitlab")
	}
}

func TestRead_parsesVCSServer(t *testing.T) {
	dir := t.TempDir()
	yaml := "vcs:\n  provider: gitlab\n  server: https://gitlab.company.com\n"
	if err := os.WriteFile(filepath.Join(dir, ".agentctl.yml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Read(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.VCS.Provider != "gitlab" {
		t.Errorf("VCS.Provider = %q, want %q", cfg.VCS.Provider, "gitlab")
	}
	if cfg.VCS.Server != "https://gitlab.company.com" {
		t.Errorf("VCS.Server = %q, want %q", cfg.VCS.Server, "https://gitlab.company.com")
	}
}

func TestRead_vcsDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".agentctl.yml"), []byte("dev_server: \"npm start\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Read(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.VCS.Provider != "" {
		t.Errorf("VCS.Provider = %q, want empty string (default)", cfg.VCS.Provider)
	}
	if cfg.VCS.Server != "" {
		t.Errorf("VCS.Server = %q, want empty string (default)", cfg.VCS.Server)
	}
}

func TestWrite_preservesVCS(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.AgentctlConfig{
		VCS: config.VCSConfig{
			Provider: "gitlab",
			Server:   "https://gitlab.company.com",
		},
	}
	if err := config.Write(dir, cfg); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	got, err := config.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.VCS.Provider != "gitlab" {
		t.Errorf("VCS.Provider = %q, want %q", got.VCS.Provider, "gitlab")
	}
	if got.VCS.Server != "https://gitlab.company.com" {
		t.Errorf("VCS.Server = %q, want %q", got.VCS.Server, "https://gitlab.company.com")
	}
}

// ─── GlobalPath ───────────────────────────────────────────────────────────────

func TestGlobalPath_usesXDGConfigHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	got := config.GlobalPath()
	want := filepath.Join(tmp, "agentctl", "config.yml")
	if got != want {
		t.Errorf("GlobalPath() = %q, want %q", got, want)
	}
}

// ─── ReadGlobal ───────────────────────────────────────────────────────────────

func TestReadGlobal_absentFile_returnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	cfg, err := config.ReadGlobal()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DefaultAgent != "" || cfg.Notify != nil || cfg.MergeStrategy != "" || cfg.Editor != "" {
		t.Errorf("expected empty GlobalConfig for absent file, got %+v", cfg)
	}
}

func TestReadGlobal_parsesFields(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	dir := filepath.Join(tmp, "agentctl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "default_agent: codex\nnotify: true\nmerge_strategy: squash\neditor: cursor\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ReadGlobal()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DefaultAgent != "codex" {
		t.Errorf("DefaultAgent = %q, want %q", cfg.DefaultAgent, "codex")
	}
	if cfg.Notify == nil || !*cfg.Notify {
		t.Errorf("Notify = %v, want true", cfg.Notify)
	}
	if cfg.MergeStrategy != "squash" {
		t.Errorf("MergeStrategy = %q, want %q", cfg.MergeStrategy, "squash")
	}
	if cfg.Editor != "cursor" {
		t.Errorf("Editor = %q, want %q", cfg.Editor, "cursor")
	}
}

func TestReadGlobal_malformedFile_returnsError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	dir := filepath.Join(tmp, "agentctl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(":\tinvalid:\tyaml\t:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.ReadGlobal()
	if err == nil {
		t.Error("expected error for malformed global config, got nil")
	}
}

// ─── WriteGlobal ──────────────────────────────────────────────────────────────

func TestWriteGlobal_roundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	in := &config.GlobalConfig{
		DefaultAgent:  "codex",
		Notify:        boolPtr(true),
		MergeStrategy: "rebase",
		Editor:        "code",
	}
	if err := config.WriteGlobal(in); err != nil {
		t.Fatalf("WriteGlobal error: %v", err)
	}
	got, err := config.ReadGlobal()
	if err != nil {
		t.Fatalf("ReadGlobal error: %v", err)
	}
	if got.DefaultAgent != "codex" {
		t.Errorf("DefaultAgent = %q, want %q", got.DefaultAgent, "codex")
	}
	if got.Notify == nil || !*got.Notify {
		t.Errorf("Notify = %v, want true", got.Notify)
	}
	if got.MergeStrategy != "rebase" {
		t.Errorf("MergeStrategy = %q, want %q", got.MergeStrategy, "rebase")
	}
	if got.Editor != "code" {
		t.Errorf("Editor = %q, want %q", got.Editor, "code")
	}
}

func TestWriteGlobal_createsParentDirs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	// Parent dir does not exist yet — WriteGlobal must create it.
	in := &config.GlobalConfig{DefaultAgent: "gemini"}
	if err := config.WriteGlobal(in); err != nil {
		t.Fatalf("WriteGlobal error: %v", err)
	}
	if _, err := os.Stat(config.GlobalPath()); err != nil {
		t.Errorf("global config file not created: %v", err)
	}
}

// ─── ReadMerged ───────────────────────────────────────────────────────────────

func TestReadMerged_noFiles_returnsDefaults(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	cfg, err := config.ReadMerged(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DefaultAgent != "" || cfg.MergeStrategy != "" || cfg.Editor != "" {
		t.Errorf("expected zero values for missing files, got %+v", cfg)
	}
}

func TestReadMerged_globalOnly_returnsGlobalValues(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	globalDir := filepath.Join(tmp, "agentctl")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.yml"), []byte("default_agent: codex\nmerge_strategy: squash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	projectDir := t.TempDir()
	cfg, err := config.ReadMerged(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DefaultAgent != "codex" {
		t.Errorf("DefaultAgent = %q, want %q", cfg.DefaultAgent, "codex")
	}
	if cfg.MergeStrategy != "squash" {
		t.Errorf("MergeStrategy = %q, want %q", cfg.MergeStrategy, "squash")
	}
}

func TestReadMerged_projectLocalOnly_returnsLocalValues(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, ".agentctl.yml"), []byte("default_agent: gemini\nmerge_strategy: rebase\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ReadMerged(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DefaultAgent != "gemini" {
		t.Errorf("DefaultAgent = %q, want %q", cfg.DefaultAgent, "gemini")
	}
	if cfg.MergeStrategy != "rebase" {
		t.Errorf("MergeStrategy = %q, want %q", cfg.MergeStrategy, "rebase")
	}
}

func TestReadMerged_projectLocalWinsOnNonZeroField(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	globalDir := filepath.Join(tmp, "agentctl")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.yml"), []byte("default_agent: codex\nmerge_strategy: squash\neditor: vim\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, ".agentctl.yml"), []byte("default_agent: gemini\neditor: cursor\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ReadMerged(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DefaultAgent != "gemini" {
		t.Errorf("DefaultAgent = %q, want %q (project-local should win)", cfg.DefaultAgent, "gemini")
	}
	if cfg.Editor != "cursor" {
		t.Errorf("Editor = %q, want %q (project-local should win)", cfg.Editor, "cursor")
	}
	// Global fills gaps where project-local is empty.
	if cfg.MergeStrategy != "squash" {
		t.Errorf("MergeStrategy = %q, want %q (global fills gap)", cfg.MergeStrategy, "squash")
	}
}

func TestReadMerged_globalFillsGaps(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	globalDir := filepath.Join(tmp, "agentctl")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.yml"), []byte("default_agent: codex\nmerge_strategy: squash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, ".agentctl.yml"), []byte("dev_server: \"npm run dev\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ReadMerged(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Project-local field preserved.
	if cfg.DevServer != "npm run dev" {
		t.Errorf("DevServer = %q, want %q", cfg.DevServer, "npm run dev")
	}
	// Global fills missing fields.
	if cfg.DefaultAgent != "codex" {
		t.Errorf("DefaultAgent = %q, want %q (global fills gap)", cfg.DefaultAgent, "codex")
	}
	if cfg.MergeStrategy != "squash" {
		t.Errorf("MergeStrategy = %q, want %q (global fills gap)", cfg.MergeStrategy, "squash")
	}
}

func TestReadMerged_malformedGlobal_returnsError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	globalDir := filepath.Join(tmp, "agentctl")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.yml"), []byte(":\tinvalid:\tyaml\t:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	projectDir := t.TempDir()
	_, err := config.ReadMerged(projectDir)
	if err == nil {
		t.Error("expected error for malformed global config, got nil")
	}
}

func TestReadMerged_notifyGlobalTrue_absentInLocal_effectiveTrue(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	globalDir := filepath.Join(tmp, "agentctl")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.yml"), []byte("notify: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	projectDir := t.TempDir()
	cfg, err := config.ReadMerged(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Notify == nil || !*cfg.Notify {
		t.Errorf("Notify = %v, want true (global fills gap)", cfg.Notify)
	}
}

func TestReadMerged_notifyFalseInLocal_overridesGlobalTrue(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	globalDir := filepath.Join(tmp, "agentctl")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.yml"), []byte("notify: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, ".agentctl.yml"), []byte("notify: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ReadMerged(projectDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Notify == nil || *cfg.Notify {
		t.Errorf("Notify = %v, want false (project-local explicit false overrides global true)", cfg.Notify)
	}
}
