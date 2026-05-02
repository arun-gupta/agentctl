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
