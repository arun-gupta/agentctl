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
	if cfg.DevServer != "" || cfg.Port != 0 {
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

func TestWrite_persistsPort(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".agentctl.yml"), []byte("dev_server: \"uvicorn main:app --port {port}\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Port = 3042
	if err := config.Write(dir, cfg); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	got, err := config.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 3042 {
		t.Errorf("Port = %d, want 3042", got.Port)
	}
	if got.DevServer != "uvicorn main:app --port {port}" {
		t.Errorf("DevServer lost after Write: %q", got.DevServer)
	}
}

func TestWrite_createsFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.AgentctlConfig{Port: 3010}
	if err := config.Write(dir, cfg); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	got, err := config.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 3010 {
		t.Errorf("Port = %d, want 3010", got.Port)
	}
}
