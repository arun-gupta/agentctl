// Package diagnostics writes and reads per-run structured metadata for agentctl.
// Each agent run is persisted as a JSON file under .agentctl/runs/<issue>-<ts>.json
// inside the repo, and a single-line JSON entry is appended to
// ~/.config/agentctl/run-history.jsonl for cross-repo reporting.
package diagnostics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/arun-gupta/agentctl/internal/xdg"
)

const (
	runsSubdir        = ".agentctl/runs"
	globalHistoryFile = "run-history.jsonl"
)

// RunRecord captures structured metadata for a single agent run.
type RunRecord struct {
	Issue          string     `json:"issue"`
	Branch         string     `json:"branch"`
	Agent          string     `json:"agent"`
	StartedAt      time.Time  `json:"started_at"`
	StoppedAt      *time.Time `json:"stopped_at,omitempty"`
	ElapsedSeconds float64    `json:"elapsed_seconds,omitempty"`
	// ExitReason is one of: pr_opened, discarded, failed, in_progress.
	ExitReason   string `json:"exit_reason,omitempty"`
	PRURL        string `json:"pr_url,omitempty"`
	FilesChanged int    `json:"files_changed,omitempty"`
	TokensUsed   int64  `json:"tokens_used,omitempty"`
	Template     string `json:"template,omitempty"`
	Scheduled    bool   `json:"scheduled,omitempty"`
}

// RecordFilename returns the base filename for a run record: "<issue>-<ts>.json".
func RecordFilename(issue string, startedAt time.Time) string {
	ts := startedAt.UTC().Format("20060102T150405Z")
	return safeIdentifier(issue) + "-" + ts + ".json"
}

func safeIdentifier(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "run"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return "run"
	}
	return out
}

// Write creates (or overwrites) a run record in <repoRoot>/.agentctl/runs/.
// It returns the base filename so callers can store it in the .agent state file.
func Write(repoRoot string, r *RunRecord) (string, error) {
	dir := filepath.Join(repoRoot, runsSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	filename := RecordFilename(r.Issue, r.StartedAt)
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, filename), data, 0o644); err != nil {
		return "", err
	}
	return filename, nil
}

// Update reads the named file from <repoRoot>/.agentctl/runs/, applies fn to
// the record, and writes it back. It is a no-op when the file does not exist.
func Update(repoRoot, filename string, fn func(*RunRecord)) error {
	path := filepath.Join(repoRoot, runsSubdir, filename)
	r, err := Read(repoRoot, filename)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	fn(&r)
	out, err := json.MarshalIndent(&r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// Read reads and unmarshals a named run record from <repoRoot>/.agentctl/runs/.
func Read(repoRoot, filename string) (RunRecord, error) {
	path := filepath.Join(repoRoot, runsSubdir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return RunRecord{}, err
	}
	var r RunRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return RunRecord{}, err
	}
	return r, nil
}

// List reads all run records from <repoRoot>/.agentctl/runs/, returning them
// sorted by StartedAt ascending. An absent directory returns nil, nil.
func List(repoRoot string) ([]*RunRecord, error) {
	dir := filepath.Join(repoRoot, runsSubdir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []*RunRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			return nil, fmt.Errorf("read run record %q: %w", e.Name(), readErr)
		}
		var r RunRecord
		if jsonErr := json.Unmarshal(data, &r); jsonErr != nil {
			return nil, fmt.Errorf("parse run record %q: %w", e.Name(), jsonErr)
		}
		records = append(records, &r)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].StartedAt.Before(records[j].StartedAt)
	})
	return records, nil
}

// AppendGlobal appends r as a single newline-terminated JSON line to
// ~/.config/agentctl/run-history.jsonl (created if absent).
func AppendGlobal(r *RunRecord) error {
	cfgDir := xdg.UserConfigDir()
	if cfgDir == "" {
		return nil // silently skip when config dir is unavailable
	}
	dir := filepath.Join(cfgDir, "agentctl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, globalHistoryFile)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}
