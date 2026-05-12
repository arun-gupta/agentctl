package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arun-gupta/agentctl/internal/config"
)

func TestResolveEditor(t *testing.T) {
	tests := []struct {
		name       string
		flagEditor string
		cfg        *config.AgentctlConfig
		env        map[string]string
		want       string
	}{
		{
			name:       "flag takes precedence over all",
			flagEditor: "code",
			cfg:        &config.AgentctlConfig{Editor: "cursor"},
			env:        map[string]string{"AGENTCTL_EDITOR": "vim", "VISUAL": "nano", "EDITOR": "ed"},
			want:       "code",
		},
		{
			name:       "config overrides env vars",
			flagEditor: "",
			cfg:        &config.AgentctlConfig{Editor: "cursor"},
			env:        map[string]string{"AGENTCTL_EDITOR": "vim", "VISUAL": "nano", "EDITOR": "ed"},
			want:       "cursor",
		},
		{
			name:       "AGENTCTL_EDITOR overrides VISUAL and EDITOR",
			flagEditor: "",
			cfg:        &config.AgentctlConfig{},
			env:        map[string]string{"AGENTCTL_EDITOR": "vim", "VISUAL": "nano", "EDITOR": "ed"},
			want:       "vim",
		},
		{
			name:       "VISUAL overrides EDITOR",
			flagEditor: "",
			cfg:        &config.AgentctlConfig{},
			env:        map[string]string{"VISUAL": "nano", "EDITOR": "ed"},
			want:       "nano",
		},
		{
			name:       "EDITOR is last fallback",
			flagEditor: "",
			cfg:        &config.AgentctlConfig{},
			env:        map[string]string{"EDITOR": "ed"},
			want:       "ed",
		},
		{
			name:       "empty when no editor set",
			flagEditor: "",
			cfg:        &config.AgentctlConfig{},
			env:        map[string]string{},
			want:       "",
		},
		{
			name:       "nil config is safe",
			flagEditor: "",
			cfg:        nil,
			env:        map[string]string{"EDITOR": "vi"},
			want:       "vi",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"AGENTCTL_EDITOR", "VISUAL", "EDITOR"} {
				t.Setenv(k, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got := resolveEditor(tt.flagEditor, tt.cfg)
			if got != tt.want {
				t.Errorf("resolveEditor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOpenWorktree_pathFlag(t *testing.T) {
	var buf bytes.Buffer
	if err := openWorktree("/tmp/worktree-42", "", true, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	if got != "/tmp/worktree-42" {
		t.Errorf("openWorktree --path = %q, want %q", got, "/tmp/worktree-42")
	}
}

func TestOpenWorktree_noEditorFallback(t *testing.T) {
	var buf bytes.Buffer
	if err := openWorktree("/tmp/worktree-42", "", false, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	if got != "/tmp/worktree-42" {
		t.Errorf("openWorktree no-editor fallback = %q, want %q", got, "/tmp/worktree-42")
	}
}

func TestOpenWorktree_badEditor(t *testing.T) {
	var buf bytes.Buffer
	err := openWorktree("/tmp/worktree-42", "no-such-editor-xyz-abc-999", false, &buf)
	if err == nil {
		t.Fatal("expected error for nonexistent editor, got nil")
	}
	if !strings.Contains(err.Error(), "cannot open editor") {
		t.Errorf("error = %q, want it to mention 'cannot open editor'", err.Error())
	}
}

func TestOpenWorktree_editorWithArgs(t *testing.T) {
	var buf bytes.Buffer
	if err := openWorktree("/tmp/worktree-42", "echo -n", false, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveEditorForRepo_badConfig(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, ".agentctl.yml"), []byte("editor: ["), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := resolveEditorForRepo("", repoRoot)
	if err == nil {
		t.Fatal("expected error for malformed config, got nil")
	}
	if !strings.Contains(err.Error(), "cannot read config") {
		t.Fatalf("error = %q, want it to mention cannot read config", err)
	}
}

func TestResolveEditorForRepo_flagSkipsMalformedConfig(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, ".agentctl.yml"), []byte("editor: ["), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got, err := resolveEditorForRepo("code", repoRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "code" {
		t.Fatalf("resolveEditorForRepo() = %q, want %q", got, "code")
	}
}
