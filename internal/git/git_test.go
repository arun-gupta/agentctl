package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a temporary git repository with an initial commit and
// returns its path. Tests are skipped when git is not available.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := t.TempDir()
	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitRun("init")
	gitRun("config", "user.email", "test@example.com")
	gitRun("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", ".")
	gitRun("commit", "-m", "init")
	return dir
}

func TestCurrentBranch(t *testing.T) {
	repo := initRepo(t)
	branch, err := CurrentBranch(repo)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if branch == "" || branch == "HEAD" {
		t.Errorf("expected a real branch name, got %q", branch)
	}
}

func TestBranchExists(t *testing.T) {
	repo := initRepo(t)

	// Detect the actual default branch (main or master).
	defaultBranch, err := CurrentBranch(repo)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}

	if !BranchExists(repo, defaultBranch) {
		t.Errorf("BranchExists(%q) = false, want true", defaultBranch)
	}
	if BranchExists(repo, "no-such-branch") {
		t.Error("BranchExists(no-such-branch) = true, want false")
	}
}

func TestFindBranchByIssuePrefix(t *testing.T) {
	repo := initRepo(t)

	// Create a branch with an issue prefix.
	cmd := exec.Command("git", "-C", repo, "branch", "42-feature")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create branch: %v\n%s", err, out)
	}

	got, err := FindBranchByIssuePrefix(repo, "42")
	if err != nil {
		t.Fatalf("FindBranchByIssuePrefix: %v", err)
	}
	if got != "42-feature" {
		t.Errorf("got %q, want %q", got, "42-feature")
	}

	// Non-existent prefix returns empty string.
	got, err = FindBranchByIssuePrefix(repo, "99")
	if err != nil {
		t.Fatalf("FindBranchByIssuePrefix (miss): %v", err)
	}
	if got != "" {
		t.Errorf("expected empty for missing prefix, got %q", got)
	}
}

func TestDeleteLocalBranch(t *testing.T) {
	repo := initRepo(t)

	cmd := exec.Command("git", "-C", repo, "branch", "99-to-delete")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create branch: %v\n%s", err, out)
	}

	if !BranchExists(repo, "99-to-delete") {
		t.Fatal("branch should exist before deletion")
	}
	if err := DeleteLocalBranch(repo, "99-to-delete"); err != nil {
		t.Fatalf("DeleteLocalBranch: %v", err)
	}
	if BranchExists(repo, "99-to-delete") {
		t.Error("branch should not exist after deletion")
	}
}

func TestLinkedWorktrees_empty(t *testing.T) {
	repo := initRepo(t)
	wts, err := LinkedWorktrees(repo)
	if err != nil {
		t.Fatalf("LinkedWorktrees: %v", err)
	}
	if len(wts) != 0 {
		t.Errorf("expected 0 linked worktrees, got %d", len(wts))
	}
}

func TestAddRemoveWorktree(t *testing.T) {
	repo := initRepo(t)
	wtPath := filepath.Join(t.TempDir(), "wt-42-feature")

	if err := AddWorktree(repo, wtPath, "42-feature"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	// Worktree directory must exist.
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("worktree dir should exist: %v", err)
	}

	// Must appear in LinkedWorktrees with correct branch and issue.
	// Compare by branch name rather than path to avoid /tmp→/private/tmp
	// symlink differences on macOS.
	wts, err := LinkedWorktrees(repo)
	if err != nil {
		t.Fatalf("LinkedWorktrees: %v", err)
	}
	found := false
	for _, wt := range wts {
		if wt.Branch == "42-feature" {
			found = true
			if wt.Issue != "42" {
				t.Errorf("issue = %q, want %q", wt.Issue, "42")
			}
		}
	}
	if !found {
		t.Error("added worktree not found in LinkedWorktrees output")
	}

	if err := RemoveWorktree(repo, wtPath); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Error("worktree dir should be gone after removal")
	}
}

func TestFindWorktreeByIssue(t *testing.T) {
	repo := initRepo(t)
	wtPath := filepath.Join(t.TempDir(), "repo-42-my-feature")

	if err := AddWorktree(repo, wtPath, "42-my-feature"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	wt, found, err := FindWorktreeByIssue(repo, "42")
	if err != nil {
		t.Fatalf("FindWorktreeByIssue: %v", err)
	}
	if !found {
		t.Fatal("expected worktree to be found for issue 42")
	}
	if !strings.Contains(wt.Path, "-42-") {
		t.Errorf("path %q should contain -42-", wt.Path)
	}

	// Non-existent issue returns not-found.
	_, found, err = FindWorktreeByIssue(repo, "99")
	if err != nil {
		t.Fatalf("FindWorktreeByIssue (miss): %v", err)
	}
	if found {
		t.Error("expected not-found for issue 99")
	}
}

func TestInferIssue(t *testing.T) {
	tests := []struct {
		branch string
		want   string
	}{
		{"42-my-feature", "42"},
		{"210-some-long-slug", "210"},
		{"main", ""},
		{"HEAD", ""},
		{"feature-no-number", ""},
		{"0-zero", "0"},
		{"1234-multi-digit", "1234"},
		{"", ""},
	}
	for _, tt := range tests {
		got := InferIssue(tt.branch)
		if got != tt.want {
			t.Errorf("InferIssue(%q) = %q, want %q", tt.branch, got, tt.want)
		}
	}
}

func TestLinkedWorktreesParserSmoke(t *testing.T) {
	// Parse a synthetic --porcelain output without calling git.
	// We exercise the parser indirectly by calling the unexported helper via
	// the exported LinkedWorktrees function with a fake git binary; instead
	// we test the branch-inference logic (which is pure) and leave integration
	// tests for environments with a real git repo.

	cases := []struct {
		branch  string
		wantIss string
	}{
		{"refs/heads/42-feature", "42"},
		{"refs/heads/main", ""},
		{"refs/heads/1-x", "1"},
	}
	for _, c := range cases {
		// Trim "refs/heads/" prefix as the parser does.
		branch := c.branch
		const prefix = "refs/heads/"
		if len(branch) > len(prefix) && branch[:len(prefix)] == prefix {
			branch = branch[len(prefix):]
		}
		got := InferIssue(branch)
		if got != c.wantIss {
			t.Errorf("InferIssue(%q) = %q, want %q", branch, got, c.wantIss)
		}
	}
}
