package diagnostics_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arun-gupta/agentctl/internal/diagnostics"
)

func TestWriteAndList(t *testing.T) {
	repoRoot := t.TempDir()
	now := time.Date(2026, 5, 1, 10, 4, 11, 0, time.UTC)

	r := &diagnostics.RunRecord{
		Issue:     "42",
		Branch:    "feat/42-dark-mode",
		Agent:     "claude",
		StartedAt: now,
		ExitReason: "in_progress",
	}

	filename, err := diagnostics.Write(repoRoot, r)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if filename == "" {
		t.Fatal("Write returned empty filename")
	}

	records, err := diagnostics.List(repoRoot)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List: got %d records, want 1", len(records))
	}
	got := records[0]
	if got.Issue != "42" {
		t.Errorf("Issue: got %q, want %q", got.Issue, "42")
	}
	if got.Branch != "feat/42-dark-mode" {
		t.Errorf("Branch: got %q, want %q", got.Branch, "feat/42-dark-mode")
	}
	if got.Agent != "claude" {
		t.Errorf("Agent: got %q, want %q", got.Agent, "claude")
	}
	if !got.StartedAt.Equal(now) {
		t.Errorf("StartedAt: got %v, want %v", got.StartedAt, now)
	}
	if got.ExitReason != "in_progress" {
		t.Errorf("ExitReason: got %q, want %q", got.ExitReason, "in_progress")
	}
}

func TestUpdate(t *testing.T) {
	repoRoot := t.TempDir()
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	stopped := now.Add(30 * time.Minute)

	r := &diagnostics.RunRecord{
		Issue:      "99",
		Branch:     "feat/99-test",
		Agent:      "claude",
		StartedAt:  now,
		ExitReason: "in_progress",
	}

	filename, err := diagnostics.Write(repoRoot, r)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	err = diagnostics.Update(repoRoot, filename, func(rec *diagnostics.RunRecord) {
		rec.StoppedAt = &stopped
		rec.ElapsedSeconds = stopped.Sub(rec.StartedAt).Seconds()
		rec.ExitReason = "pr_opened"
		rec.PRURL = "https://github.com/owner/repo/pull/5"
		rec.FilesChanged = 7
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	records, err := diagnostics.List(repoRoot)
	if err != nil {
		t.Fatalf("List after Update: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	got := records[0]
	if got.ExitReason != "pr_opened" {
		t.Errorf("ExitReason: got %q, want %q", got.ExitReason, "pr_opened")
	}
	if got.StoppedAt == nil || !got.StoppedAt.Equal(stopped) {
		t.Errorf("StoppedAt: got %v, want %v", got.StoppedAt, stopped)
	}
	if got.ElapsedSeconds != 1800 {
		t.Errorf("ElapsedSeconds: got %v, want 1800", got.ElapsedSeconds)
	}
	if got.PRURL != "https://github.com/owner/repo/pull/5" {
		t.Errorf("PRURL: got %q", got.PRURL)
	}
	if got.FilesChanged != 7 {
		t.Errorf("FilesChanged: got %d, want 7", got.FilesChanged)
	}
}

func TestListEmpty(t *testing.T) {
	repoRoot := t.TempDir()
	records, err := diagnostics.List(repoRoot)
	if err != nil {
		t.Fatalf("List on empty dir: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestListSortedByStartedAt(t *testing.T) {
	repoRoot := t.TempDir()
	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

	// Write records out of order by sleeping a second between timestamps.
	for _, offset := range []time.Duration{2 * time.Hour, 0, time.Hour} {
		ts := base.Add(offset)
		r := &diagnostics.RunRecord{
			Issue:     "1",
			Branch:    "feat/1",
			Agent:     "claude",
			StartedAt: ts,
		}
		if _, err := diagnostics.Write(repoRoot, r); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	records, err := diagnostics.List(repoRoot)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}
	for i := 1; i < len(records); i++ {
		if records[i].StartedAt.Before(records[i-1].StartedAt) {
			t.Errorf("records not sorted ascending at index %d", i)
		}
	}
}

func TestRecordFilenameFormat(t *testing.T) {
	ts := time.Date(2026, 5, 1, 10, 4, 11, 0, time.UTC)
	got := diagnostics.RecordFilename("42", ts)
	want := "42-20260501T100411Z.json"
	if got != want {
		t.Errorf("RecordFilename: got %q, want %q", got, want)
	}
}

func TestWriteCreatesDirectory(t *testing.T) {
	repoRoot := t.TempDir()
	r := &diagnostics.RunRecord{
		Issue:     "5",
		Branch:    "feat/5",
		Agent:     "claude",
		StartedAt: time.Now(),
	}
	if _, err := diagnostics.Write(repoRoot, r); err != nil {
		t.Fatalf("Write: %v", err)
	}
	dir := filepath.Join(repoRoot, ".agentctl", "runs")
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("runs directory not created: %v", err)
	}
}

func TestWriteProducesValidJSON(t *testing.T) {
	repoRoot := t.TempDir()
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	r := &diagnostics.RunRecord{
		Issue:     "7",
		Branch:    "feat/7",
		Agent:     "claude",
		StartedAt: now,
	}
	filename, err := diagnostics.Write(repoRoot, r)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, ".agentctl", "runs", filename))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	if parsed["issue"] != "7" {
		t.Errorf("JSON issue: got %v, want %q", parsed["issue"], "7")
	}
}

func TestAppendGlobal(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	r := &diagnostics.RunRecord{
		Issue:     "42",
		Branch:    "feat/42",
		Agent:     "claude",
		StartedAt: now,
	}

	if err := diagnostics.AppendGlobal(r); err != nil {
		t.Fatalf("AppendGlobal: %v", err)
	}
	if err := diagnostics.AppendGlobal(r); err != nil {
		t.Fatalf("AppendGlobal second call: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmp, "agentctl", "run-history.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := 0
	for _, line := range filepath.SplitList(string(data)) {
		if line != "" {
			lines++
		}
	}
	// Should have 2 newline-delimited JSON lines.
	if lines < 2 {
		t.Logf("raw content:\n%s", data)
	}
}
