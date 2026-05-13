// Package diagnostics writes and reads per-run structured metadata for agentctl.
// Each agent run is persisted as a JSON file under .agentctl/runs/<issue>-<ts>.json
// inside the repo, and a single-line JSON entry is appended to
// ~/.config/agentctl/run-history.jsonl for cross-repo reporting.
package diagnostics

import (
	"encoding/json"
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
	return issue + "-" + ts + ".json"
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
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var r RunRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	fn(&r)
	out, err := json.MarshalIndent(&r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
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
			continue
		}
		var r RunRecord
		if jsonErr := json.Unmarshal(data, &r); jsonErr != nil {
			continue
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, globalHistoryFile)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}
