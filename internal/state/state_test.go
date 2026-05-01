package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadWrite(t *testing.T) {
	dir := t.TempDir()

	// A non-existent file should return a zero value, not an error.
	af, err := Read(dir)
	if err != nil {
		t.Fatalf("Read on missing file: %v", err)
	}
	if af.Agent != "" || af.SessionID != "" {
		t.Errorf("expected zero AgentFile for missing file, got %+v", af)
	}

	// Write a full state and read it back.
	want := AgentFile{
		Agent:     "claude",
		SessionID: "abc-123",
		DevPID:    "9999",
		AgentPID:  "8888",
	}
	if err := Write(dir, want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read after write: %v", err)
	}
	if got.Agent != want.Agent {
		t.Errorf("Agent: got %q, want %q", got.Agent, want.Agent)
	}
	if got.SessionID != want.SessionID {
		t.Errorf("SessionID: got %q, want %q", got.SessionID, want.SessionID)
	}
	if got.DevPID != want.DevPID {
		t.Errorf("DevPID: got %q, want %q", got.DevPID, want.DevPID)
	}
	if got.AgentPID != want.AgentPID {
		t.Errorf("AgentPID: got %q, want %q", got.AgentPID, want.AgentPID)
	}
}

func TestWriteOmitsEmptyAgentPID(t *testing.T) {
	dir := t.TempDir()
	af := AgentFile{
		Agent:     "codex",
		SessionID: "xyz-789",
		DevPID:    "1234",
		// AgentPID intentionally empty (interactive spawn)
	}
	if err := Write(dir, af); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(raw)
	if contains(content, "agent-pid=") {
		t.Error("expected agent-pid line to be absent when AgentPID is empty")
	}
}

func TestAppendKey(t *testing.T) {
	dir := t.TempDir()
	af := AgentFile{Agent: "claude", SessionID: "s1", DevPID: "100"}
	if err := Write(dir, af); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := AppendKey(dir, "agent-pid", "42"); err != nil {
		t.Fatalf("AppendKey: %v", err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.AgentPID != "42" {
		t.Errorf("AgentPID after AppendKey: got %q, want %q", got.AgentPID, "42")
	}
}

func TestGetKey(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, AgentFile{
		Agent:     "copilot",
		SessionID: "sess-999",
		DevPID:    "77",
		SDD:       "plain",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	v, err := GetKey(dir, "agent")
	if err != nil || v != "copilot" {
		t.Errorf("GetKey agent: got %q %v", v, err)
	}
	v, err = GetKey(dir, "session-id")
	if err != nil || v != "sess-999" {
		t.Errorf("GetKey session-id: got %q %v", v, err)
	}
	v, err = GetKey(dir, "sdd")
	if err != nil || v != "plain" {
		t.Errorf("GetKey sdd: got %q %v", v, err)
	}
}

func TestWriteAlwaysIncludesSDD(t *testing.T) {
	// Write must always emit the sdd= key, even when SDD is empty, so that
	// Read can set SDDSet=true and callers can distinguish new worktrees
	// created without --sdd from legacy worktrees that predate the sdd key.
	dir := t.TempDir()
	af := AgentFile{
		Agent:     "codex",
		SessionID: "xyz-789",
		DevPID:    "1234",
		// SDD intentionally empty (started without --sdd)
	}
	if err := Write(dir, af); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !contains(string(raw), "sdd=") {
		t.Error("expected sdd= line to be present even when SDD is empty")
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !got.SDDSet {
		t.Error("expected SDDSet=true after reading a file written with empty SDD")
	}
	if got.SDD != "" {
		t.Errorf("SDD: got %q, want %q", got.SDD, "")
	}
}

func TestDevPortRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := AgentFile{
		Agent:     "claude",
		SessionID: "s1",
		DevPID:    "1234",
		DevPort:   "3042",
	}
	if err := Write(dir, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.DevPort != want.DevPort {
		t.Errorf("DevPort: got %q, want %q", got.DevPort, want.DevPort)
	}
}

func TestWriteOmitsEmptyDevPort(t *testing.T) {
	dir := t.TempDir()
	af := AgentFile{
		Agent:     "claude",
		SessionID: "s1",
		DevPID:    "1234",
		// DevPort intentionally empty (no dev server)
	}
	if err := Write(dir, af); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if contains(string(raw), "dev-port=") {
		t.Error("expected dev-port line to be absent when DevPort is empty")
	}
}

func TestReadExtraKeys(t *testing.T) {
	dir := t.TempDir()
	content := "agent=claude\nsession-id=s1\ndev-pid=1\ncustom-key=hello\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	af, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if v := af.Extra["custom-key"]; v != "hello" {
		t.Errorf("Extra[custom-key]: got %q, want %q", v, "hello")
	}
}

func TestIssueArgRoundTrip(t *testing.T) {
	dir := t.TempDir()
	af := AgentFile{
		Agent:     "claude",
		SessionID: "s1",
		DevPID:    "0",
		IssueArg:  "https://github.com/owner/repo/issues/42",
	}
	if err := Write(dir, af); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.IssueArg != af.IssueArg {
		t.Errorf("IssueArg = %q, want %q", got.IssueArg, af.IssueArg)
	}
}

func TestIssueArgOmittedWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	af := AgentFile{Agent: "claude", SessionID: "s1", DevPID: "0"}
	if err := Write(dir, af); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, FileName))
	if strings.Contains(string(raw), "issue-arg") {
		t.Errorf("expected no issue-arg line when IssueArg is empty, got:\n%s", raw)
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
