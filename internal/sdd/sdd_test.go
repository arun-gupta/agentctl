package sdd_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arun-gupta/agentctl/internal/sdd"
)

// ─── built-in list / get ──────────────────────────────────────────────────────

func TestList_includesBuiltins(t *testing.T) {
	got := sdd.List()
	if len(got) == 0 {
		t.Fatal("List returned no methodologies")
	}
	found := false
	for _, name := range got {
		if name == "speckit" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("List missing expected built-in methodology 'speckit'; got %v", got)
	}
}

func TestGet_speckit_builtin(t *testing.T) {
	m, err := sdd.Get("speckit")
	if err != nil {
		t.Fatalf("Get(speckit) unexpected error: %v", err)
	}
	if m.Kickoff == "" {
		t.Error("speckit methodology Kickoff is empty")
	}
}

func TestGet_unknown(t *testing.T) {
	_, err := sdd.Get("openspec-nonexistent")
	if err == nil {
		t.Fatal("Get(unknown) expected error, got nil")
	}
	if !strings.Contains(err.Error(), "openspec-nonexistent") {
		t.Errorf("error should mention the methodology name, got: %v", err)
	}
	if !strings.Contains(err.Error(), ".agentctl/sdd/") {
		t.Errorf("error should mention drop-in path hint, got: %v", err)
	}
	if !strings.Contains(err.Error(), "available:") {
		t.Errorf("error should list available methodologies, got: %v", err)
	}
}

// ─── KickoffPrompt substitution ───────────────────────────────────────────────

func TestKickoffPrompt_substitution(t *testing.T) {
	m, err := sdd.Get("speckit")
	if err != nil {
		t.Fatal(err)
	}
	prompt := m.KickoffPrompt("42", "3010")
	if strings.Contains(prompt, "{issue}") {
		t.Error("KickoffPrompt did not substitute {issue}")
	}
	if strings.Contains(prompt, "{port}") {
		t.Error("KickoffPrompt did not substitute {port}")
	}
	if !strings.Contains(prompt, "42") {
		t.Error("KickoffPrompt missing issue number 42")
	}
	if !strings.Contains(prompt, "3010") {
		t.Error("KickoffPrompt missing port 3010")
	}
}

func TestKickoffPrompt_speckit_content(t *testing.T) {
	m, err := sdd.Get("speckit")
	if err != nil {
		t.Fatal(err)
	}
	prompt := m.KickoffPrompt("99", "3020")
	if !strings.Contains(prompt, "99") {
		t.Errorf("prompt missing issue number, got: %s", prompt)
	}
	if !strings.Contains(prompt, "3020") {
		t.Errorf("prompt missing port, got: %s", prompt)
	}
	// Verify speckit lifecycle mentions are present
	if !strings.Contains(prompt, "/speckit.specify") {
		t.Errorf("speckit prompt missing /speckit.specify, got: %s", prompt)
	}
}

// ─── SkipPrompt ───────────────────────────────────────────────────────────────

func TestSkipPrompt_substitution(t *testing.T) {
	prompt := sdd.SkipPrompt("42", "3010")
	if strings.Contains(prompt, "{issue}") {
		t.Error("SkipPrompt did not substitute {issue}")
	}
	if strings.Contains(prompt, "{port}") {
		t.Error("SkipPrompt did not substitute {port}")
	}
	if !strings.Contains(prompt, "42") {
		t.Error("SkipPrompt missing issue number 42")
	}
	if !strings.Contains(prompt, "3010") {
		t.Error("SkipPrompt missing port 3010")
	}
}

func TestSkipPrompt_sameRegardlessOfMethodology(t *testing.T) {
	// SkipPrompt is always the same generic text, not methodology-specific.
	p1 := sdd.SkipPrompt("1", "3010")
	p2 := sdd.SkipPrompt("1", "3010")
	if p1 != p2 {
		t.Errorf("SkipPrompt is not deterministic: %q vs %q", p1, p2)
	}
	if !strings.Contains(p1, "Skip the SDD lifecycle") {
		t.Errorf("SkipPrompt missing generic skip text, got: %s", p1)
	}
}

// ─── resolution chain ─────────────────────────────────────────────────────────

func TestGet_userLevelOverridesBuiltin(t *testing.T) {
	cfgDir := t.TempDir()
	writeMethodology(t, filepath.Join(cfgDir, "agentctl", "sdd"), "speckit.yml",
		"kickoff: custom speckit kickoff\n")
	t.Setenv("XDG_CONFIG_HOME", cfgDir)

	m, err := sdd.Get("speckit")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Kickoff, "custom speckit kickoff") {
		t.Errorf("expected user-level to override built-in, got Kickoff=%q", m.Kickoff)
	}
}

func TestGet_projectLocalOverridesUserLevel(t *testing.T) {
	tmpDir := t.TempDir()
	writeMethodology(t, filepath.Join(tmpDir, ".agentctl", "sdd"), "mymethod.yml",
		"kickoff: project-local kickoff\n")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	m, err := sdd.Get("mymethod")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Kickoff, "project-local kickoff") {
		t.Errorf("expected project-local to win, got Kickoff=%q", m.Kickoff)
	}
}

func TestGet_duplicateExtension_ymlWins(t *testing.T) {
	tmpDir := t.TempDir()
	sddDir := filepath.Join(tmpDir, ".agentctl", "sdd")
	writeMethodology(t, sddDir, "dup.yml", "kickoff: from-yml\n")
	writeMethodology(t, sddDir, "dup.yaml", "kickoff: from-yaml\n")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	var m *sdd.Methodology
	stderr := captureStderr(t, func() {
		var getErr error
		m, getErr = sdd.Get("dup")
		if getErr != nil {
			t.Errorf("Get(dup) unexpected error: %v", getErr)
		}
	})

	if m == nil {
		t.Fatal("expected methodology, got nil")
	}
	if !strings.Contains(m.Kickoff, "from-yml") {
		t.Errorf("expected .yml to win over .yaml, got Kickoff=%q", m.Kickoff)
	}
	if !strings.Contains(stderr, "warning") || !strings.Contains(stderr, ".yml") {
		t.Errorf("expected duplicate-extension warning on stderr, got: %q", stderr)
	}
}

func TestList_userLevelShadowsBuiltin(t *testing.T) {
	cfgDir := t.TempDir()
	sddDir := filepath.Join(cfgDir, "agentctl", "sdd")
	writeMethodology(t, sddDir, "speckit.yml", "kickoff: custom speckit\n")
	writeMethodology(t, sddDir, "mymethod.yml", "kickoff: my method\n")
	t.Setenv("XDG_CONFIG_HOME", cfgDir)

	names := sdd.List()
	index := make(map[string]bool, len(names))
	for _, n := range names {
		index[n] = true
	}
	if !index["speckit"] {
		t.Error("List should include 'speckit'")
	}
	if !index["mymethod"] {
		t.Error("List should include user-defined 'mymethod'")
	}
	// speckit should appear only once
	count := 0
	for _, n := range names {
		if n == "speckit" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("'speckit' should appear exactly once in List, got %d", count)
	}
}

// ─── YAML load validation ─────────────────────────────────────────────────────

func TestLoad_missingKickoff(t *testing.T) {
	_, err := sdd.LoadBytes([]byte("# no kickoff field\n"), "test-fixture")
	if err == nil {
		t.Fatal("expected error for methodology missing 'kickoff', got nil")
	}
	if !strings.Contains(err.Error(), "kickoff") {
		t.Errorf("error should mention 'kickoff', got: %v", err)
	}
}

func TestLoad_invalidYAML(t *testing.T) {
	_, err := sdd.LoadBytes([]byte("kickoff: [\n"), "bad.yml")
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestLoad_unknownFieldsIgnored(t *testing.T) {
	// Unknown fields should be ignored for forward compatibility.
	_, err := sdd.LoadBytes([]byte("kickoff: hello\nunknown_future_field: 42\n"), "test.yml")
	if err != nil {
		t.Errorf("unknown fields should be ignored, got error: %v", err)
	}
}

func TestLoad_fromTestdata(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "minimal.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := sdd.LoadBytes(data, "testdata/minimal.yml")
	if err != nil {
		t.Fatal(err)
	}
	prompt := m.KickoffPrompt("7", "3015")
	if !strings.Contains(prompt, "7") {
		t.Errorf("KickoffPrompt missing issue, got: %s", prompt)
	}
	if !strings.Contains(prompt, "3015") {
		t.Errorf("KickoffPrompt missing port, got: %s", prompt)
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func writeMethodology(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = old })

	fn()

	w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	r.Close()
	return buf.String()
}
