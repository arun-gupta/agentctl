package adapters_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arun-gupta/agentctl/internal/adapters"
)

var knownBuiltins = []string{"claude", "codex", "copilot", "gemini", "opencode", "openhands"}

// ─── built-in list / get ──────────────────────────────────────────────────────

func TestList_includesBuiltins(t *testing.T) {
	got := adapters.List()
	if len(got) == 0 {
		t.Fatal("List returned no adapters")
	}
	index := make(map[string]bool, len(got))
	for _, name := range got {
		index[name] = true
	}
	for _, want := range knownBuiltins {
		if !index[want] {
			t.Errorf("List missing expected built-in adapter %q; got %v", want, got)
		}
	}
}

func TestGet_knownBuiltins(t *testing.T) {
	for _, name := range knownBuiltins {
		a, err := adapters.Get(name)
		if err != nil {
			t.Errorf("Get(%q) unexpected error: %v", name, err)
			continue
		}
		if a.Binary == "" {
			t.Errorf("Get(%q) returned adapter with empty Binary", name)
		}
	}
}

func TestGet_unknown(t *testing.T) {
	_, err := adapters.Get("nonexistent")
	if err == nil {
		t.Fatal("Get(nonexistent) expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown adapter") {
		t.Errorf("error should mention 'unknown adapter', got: %v", err)
	}
}

// ─── LaunchCmd / ResumeCmd table-driven ──────────────────────────────────────

func TestLaunchCmd_claude(t *testing.T) {
	a, err := adapters.Get("claude")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.LaunchCmd("do the thing", "sess-123", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "claude")
	assertContains(t, args, "do the thing")
	assertContains(t, args, "sess-123")
	assertContains(t, args, "--permission-mode")
	assertContains(t, args, "bypassPermissions")
}

func TestResumeCmd_claude(t *testing.T) {
	a, err := adapters.Get("claude")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.ResumeCmd("my feedback", "sess-123", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "claude")
	assertContains(t, args, "my feedback")
	assertContains(t, args, "sess-123")
	assertContains(t, args, "--resume")
}

func TestLaunchCmd_gemini_noSessionFlag(t *testing.T) {
	a, err := adapters.Get("gemini")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.LaunchCmd("do gemini stuff", "sess-abc", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "gemini")
	assertContains(t, args, "do gemini stuff")
	// session_type: directory — no session ID flag expected
	for _, arg := range args {
		if arg == "sess-abc" {
			t.Errorf("gemini LaunchCmd should not pass session ID as flag, got args: %v", args)
		}
	}
}

func TestResumeCmd_gemini_noSessionFlag(t *testing.T) {
	a, err := adapters.Get("gemini")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.ResumeCmd("revise this", "sess-abc", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "gemini")
	for _, arg := range args {
		if arg == "sess-abc" {
			t.Errorf("gemini ResumeCmd should not pass session ID, got args: %v", args)
		}
	}
}

func TestLaunchCmd_codex_usesExecTemplate(t *testing.T) {
	a, err := adapters.Get("codex")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.LaunchCmd("write tests", "sess-xyz", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "codex")
	assertContains(t, args, "exec")
	assertContains(t, args, "--dangerously-bypass-approvals-and-sandbox")
	assertAdjacentArgs(t, args, "-C", "/tmp/wt")
	assertContains(t, args, "write tests")
	for _, arg := range args {
		if arg == "-q" {
			t.Errorf("codex LaunchCmd must not pass -q flag, got args: %v", args)
		}
		if arg == "--session" {
			t.Errorf("codex LaunchCmd must not pass --session flag, got args: %v", args)
		}
		if arg == "sess-xyz" {
			t.Errorf("codex LaunchCmd must not pass session ID, got args: %v", args)
		}
	}
}

func TestResumeCmd_codex_usesExecResumeTemplate(t *testing.T) {
	a, err := adapters.Get("codex")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.ResumeCmd("fix the bug", "sess-xyz", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "codex")
	assertContains(t, args, "exec")
	assertContains(t, args, "resume")
	assertContains(t, args, "--last")
	assertContains(t, args, "--dangerously-bypass-approvals-and-sandbox")
	assertContains(t, args, "fix the bug")
	for _, arg := range args {
		if arg == "--resume" {
			t.Errorf("codex ResumeCmd must not pass --resume flag, got args: %v", args)
		}
		if arg == "--session" {
			t.Errorf("codex ResumeCmd must not pass --session flag, got args: %v", args)
		}
		if arg == "sess-xyz" {
			t.Errorf("codex ResumeCmd must not pass session ID, got args: %v", args)
		}
	}
}

func TestLaunchCmd_opencode_multiTokenBinary(t *testing.T) {
	a, err := adapters.Get("opencode")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.LaunchCmd("build it", "sess-oc", "/tmp/wt")
	// Path should point to "opencode" binary, "run" should be first arg
	if !strings.HasSuffix(cmd.Path, "opencode") {
		t.Errorf("opencode LaunchCmd Path should be opencode, got %q", cmd.Path)
	}
	args := cmd.Args
	assertContains(t, args, "run")
	assertContains(t, args, "build it")
}

func TestLaunchCmd_opencode_noPromptFlag(t *testing.T) {
	// opencode uses -p for --password, not for the message; kickoff must be
	// passed as a positional argument via the launch template, never via -p.
	a, err := adapters.Get("opencode")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.LaunchCmd("do the work", "sess-oc", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "run")
	assertContains(t, args, "do the work")
	for _, arg := range args {
		if arg == "-p" {
			t.Errorf("opencode LaunchCmd must not pass -p (--password flag), got args: %v", args)
		}
		if arg == "--session" {
			t.Errorf("opencode LaunchCmd must not pass --session flag, got args: %v", args)
		}
		if arg == "sess-oc" {
			t.Errorf("opencode LaunchCmd must not pass session ID as flag, got args: %v", args)
		}
	}
}

func TestResumeCmd_opencode(t *testing.T) {
	// opencode resume must use --continue and must not pass a session flag.
	a, err := adapters.Get("opencode")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.ResumeCmd("fix the bug", "sess-oc", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "run")
	assertContains(t, args, "fix the bug")
	assertContains(t, args, "--continue")
	for _, arg := range args {
		if arg == "-p" {
			t.Errorf("opencode ResumeCmd must not pass -p, got args: %v", args)
		}
		if arg == "--session" {
			t.Errorf("opencode ResumeCmd must not pass --session flag, got args: %v", args)
		}
		if arg == "sess-oc" {
			t.Errorf("opencode ResumeCmd must not pass session ID as flag, got args: %v", args)
		}
	}
}

func TestLaunchCmd_openhands(t *testing.T) {
	a, err := adapters.Get("openhands")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.LaunchCmd("fix the login bug", "sess-oh", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "openhands")
	assertContains(t, args, "--headless")
	assertContains(t, args, "--always-approve")
	assertContains(t, args, "-t")
	assertContains(t, args, "fix the login bug")
	for _, arg := range args {
		if arg == "sess-oh" {
			t.Errorf("openhands LaunchCmd must not pass session ID, got args: %v", args)
		}
	}
}

func TestResumeCmd_openhands(t *testing.T) {
	a, err := adapters.Get("openhands")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.ResumeCmd("narrow the scope", "sess-oh", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "openhands")
	assertContains(t, args, "--headless")
	assertContains(t, args, "--resume")
	assertContains(t, args, "--last")
	assertContains(t, args, "-t")
	assertContains(t, args, "narrow the scope")
	for _, arg := range args {
		if arg == "sess-oh" {
			t.Errorf("openhands ResumeCmd must not pass session ID as flag, got args: %v", args)
		}
	}
}

func TestLaunchCmd_minimal(t *testing.T) {
	a := loadTestdata(t, "minimal.yml")
	cmd := a.LaunchCmd("hello world", "sess-min", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "minbot")
	assertContains(t, args, "-p")
	assertContains(t, args, "hello world")
	// no session flag since Session field is empty
}

func TestLaunchCmd_fullOverride(t *testing.T) {
	a := loadTestdata(t, "full-override.yml")
	cmd := a.LaunchCmd("my kickoff", "sess-99", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "mybot")
	assertContains(t, args, "--init")
	assertContains(t, args, "my kickoff")
	assertContains(t, args, "--id")
	assertContains(t, args, "sess-99")
}

func TestResumeCmd_fullOverride(t *testing.T) {
	a := loadTestdata(t, "full-override.yml")
	cmd := a.ResumeCmd("please fix", "sess-99", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "mybot")
	assertContains(t, args, "--continue")
	assertContains(t, args, "please fix")
	assertContains(t, args, "--id")
	assertContains(t, args, "sess-99")
}

func TestLaunchCmd_structured(t *testing.T) {
	a := loadTestdata(t, "structured.yml")
	cmd := a.LaunchCmd("do work", "sess-st", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "structbot")
	assertContains(t, args, "-q")
	assertContains(t, args, "do work")
	assertContains(t, args, "--session")
	assertContains(t, args, "sess-st")
}

func TestResumeCmd_structured_usesResumeID(t *testing.T) {
	a := loadTestdata(t, "structured.yml")
	cmd := a.ResumeCmd("feedback", "sess-st", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "--resume")
	assertContains(t, args, "sess-st")
}

// ─── resolution chain ─────────────────────────────────────────────────────────

func TestGet_userLevelOverridesBuiltin(t *testing.T) {
	cfgDir := t.TempDir()
	writeAdapter(t, filepath.Join(cfgDir, "agentctl", "adapters"), "claude.yml", "binary: custom-claude\n")
	t.Setenv("XDG_CONFIG_HOME", cfgDir)

	a, err := adapters.Get("claude")
	if err != nil {
		t.Fatal(err)
	}
	if a.Binary != "custom-claude" {
		t.Errorf("expected user-level to override built-in, got Binary=%q", a.Binary)
	}
}

func TestGet_projectLocalOverridesUserLevel(t *testing.T) {
	tmpDir := t.TempDir()
	writeAdapter(t, filepath.Join(tmpDir, ".agentctl", "adapters"), "myagent.yml", "binary: project-myagent\n")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	a, err := adapters.Get("myagent")
	if err != nil {
		t.Fatal(err)
	}
	if a.Binary != "project-myagent" {
		t.Errorf("expected project-local to win, got Binary=%q", a.Binary)
	}
}

func TestGet_duplicateExtension_ymlWins(t *testing.T) {
	tmpDir := t.TempDir()
	adapterDir := filepath.Join(tmpDir, ".agentctl", "adapters")
	writeAdapter(t, adapterDir, "dup.yml", "binary: from-yml\n")
	writeAdapter(t, adapterDir, "dup.yaml", "binary: from-yaml\n")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	var a *adapters.Adapter
	stderr := captureStderr(t, func() {
		var getErr error
		a, getErr = adapters.Get("dup")
		if getErr != nil {
			t.Errorf("Get(dup) unexpected error: %v", getErr)
		}
	})

	if a == nil {
		t.Fatal("expected adapter, got nil")
	}
	if a.Binary != "from-yml" {
		t.Errorf("expected .yml to win over .yaml, got Binary=%q", a.Binary)
	}
	if !strings.Contains(stderr, "warning") || !strings.Contains(stderr, ".yml") {
		t.Errorf("expected duplicate-extension warning on stderr, got: %q", stderr)
	}
}

func TestList_userLevelShadowsBuiltin(t *testing.T) {
	cfgDir := t.TempDir()
	adapterDir := filepath.Join(cfgDir, "agentctl", "adapters")
	writeAdapter(t, adapterDir, "claude.yml", "binary: custom-claude\n")
	writeAdapter(t, adapterDir, "myagent.yml", "binary: myagent\n")
	t.Setenv("XDG_CONFIG_HOME", cfgDir)

	names := adapters.List()
	index := make(map[string]bool, len(names))
	for _, n := range names {
		index[n] = true
	}
	if !index["claude"] {
		t.Error("List should include 'claude'")
	}
	if !index["myagent"] {
		t.Error("List should include user-defined 'myagent'")
	}
	// claude should appear only once
	count := 0
	for _, n := range names {
		if n == "claude" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("'claude' should appear exactly once in List, got %d", count)
	}
}

// ─── YAML load validation ─────────────────────────────────────────────────────

func TestLoad_missingBinary(t *testing.T) {
	tmpDir := t.TempDir()
	writeAdapter(t, filepath.Join(tmpDir, ".agentctl", "adapters"), "nobinary.yml", "prompt: -p\n")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	_, err = adapters.Get("nobinary")
	if err == nil {
		t.Fatal("expected error for adapter missing 'binary', got nil")
	}
}

func TestLaunchCmd_copilot_noSessionFlag(t *testing.T) {
	// copilot uses session_type: directory — session continuity is implicit in
	// the worktree, so no --session-id flag should be appended.
	a, err := adapters.Get("copilot")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.LaunchCmd("do the thing", "sess-cp", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "copilot")
	assertContains(t, args, "do the thing")
	for _, arg := range args {
		if arg == "sess-cp" {
			t.Errorf("copilot LaunchCmd should not pass session ID as flag, got args: %v", args)
		}
		if arg == "--session-id" {
			t.Errorf("copilot LaunchCmd should not include --session-id, got args: %v", args)
		}
	}
}

func TestLaunchCmd_copilot_addsWorktreeDir(t *testing.T) {
	a, err := adapters.Get("copilot")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.LaunchCmd("do the thing", "sess-cp", "/tmp/wt")
	args := cmd.Args
	assertAdjacentArgs(t, args, "--add-dir", "/tmp/wt")
}

func TestResumeCmd_copilot_noSessionFlag(t *testing.T) {
	// copilot uses session_type: directory — session continuity is implicit in
	// the worktree, so no --session-id flag should be appended on resume either.
	a, err := adapters.Get("copilot")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.ResumeCmd("fix it", "sess-cp", "/tmp/wt")
	args := cmd.Args
	assertContains(t, args, "copilot")
	for _, arg := range args {
		if arg == "sess-cp" {
			t.Errorf("copilot ResumeCmd should not pass session ID, got args: %v", args)
		}
		if arg == "--session-id" {
			t.Errorf("copilot ResumeCmd should not include --session-id, got args: %v", args)
		}
	}
}

func TestResumeCmd_copilot_addsWorktreeDir(t *testing.T) {
	a, err := adapters.Get("copilot")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.ResumeCmd("fix it", "sess-cp", "/tmp/wt")
	args := cmd.Args
	assertAdjacentArgs(t, args, "--add-dir", "/tmp/wt")
}

func TestLaunchCmd_copilot_allowsExec(t *testing.T) {
	// copilot needs --allow-all-tools so shell commands are not blocked in headless mode.
	a, err := adapters.Get("copilot")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.LaunchCmd("do the thing", "sess-cp", "/tmp/wt")
	assertContains(t, cmd.Args, "--allow-all-tools")
}

func TestResumeCmd_copilot_allowsExec(t *testing.T) {
	// copilot needs --allow-all-tools on resume too so shell commands are not blocked.
	a, err := adapters.Get("copilot")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.ResumeCmd("fix it", "sess-cp", "/tmp/wt")
	assertContains(t, cmd.Args, "--allow-all-tools")
}

// ─── install hints ────────────────────────────────────────────────────────────

func TestBuiltins_installHints(t *testing.T) {
	cases := []struct {
		name    string
		binary  string
		install string
	}{
		{"claude", "claude", "npm install -g @anthropic-ai/claude-code"},
		{"gemini", "gemini", "npm install -g @google/gemini-cli"},
		{"opencode", "opencode", "npm install -g opencode@latest"},
		{"codex", "codex", "npm install -g @openai/codex"},
		{"copilot", "copilot", "npm install -g @github/copilot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := adapters.Get(tc.name)
			if err != nil {
				t.Fatalf("Get(%q): %v", tc.name, err)
			}
			first := strings.Fields(a.Binary)[0]
			if first != tc.binary {
				t.Errorf("first binary token = %q, want %q", first, tc.binary)
			}
			if a.Install != tc.install {
				t.Errorf("Install = %q, want %q", a.Install, tc.install)
			}
		})
	}
}

func TestCheckBinary_notFound(t *testing.T) {
	a, err := adapters.LoadBytes(
		[]byte("binary: __nonexistent_binary_xyz__\ninstall: npm install -g something\n"),
		"test-fixture",
	)
	if err != nil {
		t.Fatal(err)
	}
	err = a.CheckBinary()
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
	if !strings.Contains(err.Error(), "__nonexistent_binary_xyz__") {
		t.Errorf("error should mention binary name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "npm install -g something") {
		t.Errorf("error should include install hint, got: %v", err)
	}
}

func TestCheckBinary_notFound_noHint(t *testing.T) {
	a, err := adapters.LoadBytes(
		[]byte("binary: __nonexistent_binary_xyz__\n"),
		"test-fixture",
	)
	if err != nil {
		t.Fatal(err)
	}
	err = a.CheckBinary()
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
	if !strings.Contains(err.Error(), "__nonexistent_binary_xyz__") {
		t.Errorf("error should mention binary name, got: %v", err)
	}
	if strings.Contains(err.Error(), "install") {
		t.Errorf("error should not mention install when hint is absent, got: %v", err)
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// writeAdapter creates an adapter file at dir/filename with the given YAML content.
// It creates the directory if needed and fails the test on any error.
func writeAdapter(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// captureStderr temporarily redirects os.Stderr so the provided function's
// stderr writes can be inspected. Not safe for parallel tests.
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

func assertContains(t *testing.T, args []string, want string) {
	t.Helper()
	for _, a := range args {
		if a == want {
			return
		}
	}
	t.Errorf("args %v does not contain %q", args, want)
}

func assertAdjacentArgs(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i, a := range args {
		if a == flag {
			if i+1 < len(args) && args[i+1] == value {
				return
			}
			t.Errorf("args %v: %q not immediately followed by %q", args, flag, value)
			return
		}
	}
	t.Errorf("args %v does not contain %q", args, flag)
}

func loadTestdata(t *testing.T, filename string) *adapters.Adapter {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", filename))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", filename, err)
	}
	a, err := adapters.LoadBytes(data, filename)
	if err != nil {
		t.Fatalf("load testdata/%s: %v", filename, err)
	}
	return a
}
