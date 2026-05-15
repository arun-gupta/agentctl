package registry_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arun-gupta/agentctl/internal/registry"
)

// setRegistryDir overrides XDG_CONFIG_HOME to a temp directory for isolation.
func setRegistryDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func TestAdd_addsNewEntry(t *testing.T) {
	setRegistryDir(t)

	if err := registry.Add("/repos/api", "42", "42-feat-auth", "claude"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	entries, err := registry.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.RepoPath != "/repos/api" {
		t.Errorf("RepoPath = %q, want %q", e.RepoPath, "/repos/api")
	}
	if e.Issue != "42" {
		t.Errorf("Issue = %q, want %q", e.Issue, "42")
	}
	if e.Branch != "42-feat-auth" {
		t.Errorf("Branch = %q, want %q", e.Branch, "42-feat-auth")
	}
	if e.Agent != "claude" {
		t.Errorf("Agent = %q, want %q", e.Agent, "claude")
	}
	if e.AddedAt.IsZero() {
		t.Errorf("AddedAt should be set")
	}
}

func TestAdd_deduplicates(t *testing.T) {
	setRegistryDir(t)

	if err := registry.Add("/repos/api", "42", "42-feat-auth", "claude"); err != nil {
		t.Fatal(err)
	}
	// Adding the same repo+branch again is a no-op.
	if err := registry.Add("/repos/api", "42", "42-feat-auth", "codex"); err != nil {
		t.Fatal(err)
	}

	entries, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry after dedup, got %d", len(entries))
	}
	// First write wins (agent stays "claude", not "codex").
	if entries[0].Agent != "claude" {
		t.Errorf("expected agent %q, got %q", "claude", entries[0].Agent)
	}
}

func TestAdd_multipleRepos(t *testing.T) {
	setRegistryDir(t)

	if err := registry.Add("/repos/api", "42", "42-feat-auth", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Add("/repos/frontend", "17", "17-fix-login", "codex"); err != nil {
		t.Fatal(err)
	}

	entries, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestRemove_removesMatchingEntry(t *testing.T) {
	setRegistryDir(t)

	if err := registry.Add("/repos/api", "42", "42-feat-auth", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Add("/repos/frontend", "17", "17-fix-login", "codex"); err != nil {
		t.Fatal(err)
	}

	if err := registry.Remove("/repos/api", "42-feat-auth"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	entries, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after remove, got %d", len(entries))
	}
	if entries[0].Branch != "17-fix-login" {
		t.Errorf("remaining entry should be 17-fix-login, got %q", entries[0].Branch)
	}
}

func TestRemove_noopOnMissingEntry(t *testing.T) {
	setRegistryDir(t)

	// Remove on an empty registry should not error.
	if err := registry.Remove("/repos/api", "42-feat-auth"); err != nil {
		t.Fatalf("Remove on empty registry: %v", err)
	}
}

func TestList_emptyWhenNoFile(t *testing.T) {
	setRegistryDir(t)

	entries, err := registry.List()
	if err != nil {
		t.Fatalf("List on empty registry: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestRemoveStale_removesEntriesWhereWorktreeGone(t *testing.T) {
	setRegistryDir(t)

	// Create a real worktree directory so one entry is "live".
	liveDir := t.TempDir()

	if err := registry.AddWithWorktree("/repos/api", liveDir, "1", "1-live-branch", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := registry.AddWithWorktree("/repos/frontend", "/no/such/worktree", "2", "2-gone-branch", "claude"); err != nil {
		t.Fatal(err)
	}

	removed, err := registry.RemoveStale()
	if err != nil {
		t.Fatalf("RemoveStale: %v", err)
	}
	if len(removed) != 1 {
		t.Fatalf("expected 1 stale entry removed, got %d", len(removed))
	}
	if removed[0].Branch != "2-gone-branch" {
		t.Errorf("expected removed branch %q, got %q", "2-gone-branch", removed[0].Branch)
	}

	remaining, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining entry, got %d", len(remaining))
	}
	if remaining[0].Branch != "1-live-branch" {
		t.Errorf("expected remaining branch %q, got %q", "1-live-branch", remaining[0].Branch)
	}
}

func TestFilePath_usesXDGConfigHome(t *testing.T) {
	dir := setRegistryDir(t)

	if err := registry.Add("/repos/api", "42", "42-feat", "claude"); err != nil {
		t.Fatal(err)
	}

	expectedPath := filepath.Join(dir, "agentctl", "worktrees.json")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Errorf("expected registry file at %s: %v", expectedPath, err)
	}
}
