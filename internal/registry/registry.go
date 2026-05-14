// Package registry maintains the cross-repo worktree registry at
// $XDG_CONFIG_HOME/agentctl/worktrees.json.
//
// Each entry records one agentctl-managed worktree. The registry is updated
// on start, cleanup, and discard so that `agentctl status --global` can show
// active worktrees across all repositories.
package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/arun-gupta/agentctl/internal/xdg"
)

// Entry records one agentctl-managed worktree in the cross-repo registry.
type Entry struct {
	RepoPath     string    `json:"repo"`
	WorktreePath string    `json:"worktree_path"`
	Issue        string    `json:"issue"`
	Branch       string    `json:"branch"`
	Agent        string    `json:"agent"`
	AddedAt      time.Time `json:"added_at"`
}

// filePath returns the absolute path to the registry JSON file:
// $XDG_CONFIG_HOME/agentctl/worktrees.json
func filePath() string {
	dir := xdg.UserConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "agentctl", "worktrees.json")
}

// load reads the registry from disk. Returns an empty slice when the file
// does not exist.
func load() ([]Entry, error) {
	path := filePath()
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// save writes entries to disk, creating parent directories as needed.
func save(entries []Entry) error {
	path := filePath()
	if path == "" {
		return nil // no config dir available; silently skip
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Add appends an entry for the given worktree if one with the same
// repoPath+branch does not already exist. It is safe to call multiple times;
// duplicates are silently ignored.
func Add(repoPath, issue, branch, agent string) error {
	return AddWithWorktree(repoPath, "", issue, branch, agent)
}

// AddWithWorktree is like Add but also records the worktree filesystem path
// so that state can be re-verified at display time without re-deriving it.
func AddWithWorktree(repoPath, worktreePath, issue, branch, agent string) error {
	entries, err := load()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.RepoPath == repoPath && e.Branch == branch {
			return nil // already registered
		}
	}
	entries = append(entries, Entry{
		RepoPath:     repoPath,
		WorktreePath: worktreePath,
		Issue:        issue,
		Branch:       branch,
		Agent:        agent,
		AddedAt:      time.Now().UTC(),
	})
	return save(entries)
}

// Remove deletes the registry entry for the given repoPath+branch combination.
// It is a no-op when no matching entry exists.
func Remove(repoPath, branch string) error {
	entries, err := load()
	if err != nil {
		return err
	}
	n := 0
	for _, e := range entries {
		if e.RepoPath == repoPath && e.Branch == branch {
			continue
		}
		entries[n] = e
		n++
	}
	return save(entries[:n])
}

// List returns all registered entries in the order they were added.
func List() ([]Entry, error) {
	return load()
}

// RemoveStale removes entries whose WorktreePath no longer exists on disk.
// It returns the list of removed entries so callers can report them.
func RemoveStale() ([]Entry, error) {
	entries, err := load()
	if err != nil {
		return nil, err
	}
	var keep, removed []Entry
	for _, e := range entries {
		worktreePath := e.WorktreePath
		if worktreePath == "" {
			// Legacy entry without worktree_path — treat as stale.
			removed = append(removed, e)
			continue
		}
		if _, statErr := os.Stat(worktreePath); os.IsNotExist(statErr) {
			removed = append(removed, e)
		} else {
			keep = append(keep, e)
		}
	}
	return removed, save(keep)
}
