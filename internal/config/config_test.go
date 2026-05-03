package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arun-gupta/agentctl/internal/config"
)

func TestRead_missingFile_returnsEmpty(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Read(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DevServer != "" || cfg.Notify {
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
	if !cfg.Notify {
		t.Errorf("Notify = false, want true")
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
	if cfg.Notify {
		t.Errorf("Notify = true, want false (default)")
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
