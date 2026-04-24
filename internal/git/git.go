// Package git provides helpers for interacting with git worktrees and branches
// by shelling out to the git CLI. This mirrors the behaviour of the original
// agent.sh shell script so that the Go CLI has exact parity.
package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// run executes a git command in dir and returns trimmed stdout.
// stderr is discarded; the caller receives an error on non-zero exit.
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, errBuf.String())
	}
	return strings.TrimSpace(out.String()), nil
}

// RepoRoot returns the primary (main) worktree's absolute path.
// It uses `git worktree list --porcelain` and falls back to
// `git rev-parse --show-toplevel` — matching the logic in agent.sh.
func RepoRoot() (string, error) {
	out, err := run("", "worktree", "list", "--porcelain")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "worktree ") {
				return strings.TrimPrefix(line, "worktree "), nil
			}
		}
	}
	return run("", "rev-parse", "--show-toplevel")
}

// Worktree represents a single registered git worktree.
type Worktree struct {
	Path   string
	Branch string // abbreviated branch name, e.g. "42-my-feature"; "HEAD" when detached
	Issue  string // numeric prefix from branch, e.g. "42"; empty when not present
}

// issueRe matches a branch that starts with one or more digits followed by "-".
var issueRe = regexp.MustCompile(`^(\d+)-`)

// inferIssue extracts the numeric issue prefix from a branch name.
func inferIssue(branch string) string {
	if m := issueRe.FindStringSubmatch(branch); len(m) == 2 {
		return m[1]
	}
	return ""
}

// LinkedWorktrees returns all linked (non-primary) worktrees registered with
// the given primary repo root. The primary worktree itself is excluded.
func LinkedWorktrees(repoRoot string) ([]Worktree, error) {
	out, err := run(repoRoot, "-C", repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var result []Worktree
	skipFirst := true

	var curPath string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "worktree "):
			if skipFirst {
				skipFirst = false
				curPath = ""
				continue
			}
			if curPath != "" {
				// flush previous (no branch line encountered yet — detached?)
				result = append(result, Worktree{Path: curPath, Branch: "HEAD"})
			}
			curPath = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			if curPath != "" {
				raw := strings.TrimPrefix(line, "branch ")
				// branch refs/heads/42-my-feature → 42-my-feature
				branch := strings.TrimPrefix(raw, "refs/heads/")
				result = append(result, Worktree{
					Path:   curPath,
					Branch: branch,
					Issue:  inferIssue(branch),
				})
				curPath = ""
			}
		case line == "HEAD" || line == "detached":
			// bare or detached — flush with empty branch
			if curPath != "" {
				result = append(result, Worktree{Path: curPath, Branch: "HEAD"})
				curPath = ""
			}
		}
	}
	// flush trailing entry (detached HEAD or porcelain with no branch line)
	if curPath != "" {
		result = append(result, Worktree{Path: curPath, Branch: "HEAD"})
	}

	return result, nil
}

// FindWorktreeByIssue returns the first linked worktree whose path contains
// "-<issue>-" (matching the naming convention "<repo>-<issue>-<slug>").
func FindWorktreeByIssue(repoRoot, issue string) (Worktree, bool, error) {
	wts, err := LinkedWorktrees(repoRoot)
	if err != nil {
		return Worktree{}, false, err
	}
	needle := "-" + issue + "-"
	for _, wt := range wts {
		if strings.Contains(wt.Path, needle) {
			return wt, true, nil
		}
	}
	return Worktree{}, false, nil
}

// CurrentBranch returns the abbreviated branch name for the given directory.
func CurrentBranch(dir string) (string, error) {
	return run(dir, "rev-parse", "--abbrev-ref", "HEAD")
}

// IsInsideLinkedWorktree reports whether the current working directory is
// inside a linked (non-primary) worktree and returns the inferred issue number.
func IsInsideLinkedWorktree() (linked bool, issue string, err error) {
	gitDir, err := run("", "rev-parse", "--git-dir")
	if err != nil {
		return false, "", err
	}
	commonDir, err := run("", "rev-parse", "--git-common-dir")
	if err != nil {
		return false, "", err
	}
	// Canonicalise relative paths
	if !filepath.IsAbs(gitDir) {
		if abs, err2 := filepath.Abs(gitDir); err2 == nil {
			gitDir = abs
		}
	}
	if !filepath.IsAbs(commonDir) {
		if abs, err2 := filepath.Abs(commonDir); err2 == nil {
			commonDir = abs
		}
	}
	if gitDir == commonDir {
		return false, "", nil
	}
	branch, err := CurrentBranch("")
	if err != nil {
		return true, "", nil
	}
	return true, inferIssue(branch), nil
}

// AddWorktree creates a new linked worktree at path on a new branch.
func AddWorktree(repoRoot, path, branch string) error {
	_, err := run(repoRoot, "-C", repoRoot, "worktree", "add", path, "-b", branch)
	return err
}

// RemoveWorktree removes a registered linked worktree (force).
func RemoveWorktree(repoRoot, path string) error {
	_, err := run(repoRoot, "-C", repoRoot, "worktree", "remove", "--force", path)
	return err
}

// BranchExists reports whether a local branch exists in the given repo.
func BranchExists(repoRoot, branch string) bool {
	_, err := run(repoRoot, "-C", repoRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// DeleteLocalBranch force-deletes a local branch.
func DeleteLocalBranch(repoRoot, branch string) error {
	_, err := run(repoRoot, "-C", repoRoot, "branch", "-D", branch)
	return err
}

// DeleteRemoteBranch pushes a deletion to origin.  Returns the raw error
// message when the push fails so the caller can distinguish "ref does not
// exist" from real errors.
func DeleteRemoteBranch(repoRoot, branch string) (string, error) {
	cmd := exec.Command("git", "-C", repoRoot, "push", "origin", "--delete", branch)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// FindBranchByIssuePrefix returns the first local branch whose name starts
// with "<issue>-".
func FindBranchByIssuePrefix(repoRoot, issue string) (string, error) {
	out, err := run(repoRoot, "-C", repoRoot, "for-each-ref",
		"--format=%(refname:short)", "refs/heads/"+issue+"-*")
	if err != nil {
		return "", err
	}
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			return l, nil
		}
	}
	return "", nil
}

// PullFFOnly runs `git pull --ff-only origin main` in repoRoot.
func PullFFOnly(repoRoot string) error {
	_, err := run(repoRoot, "-C", repoRoot, "pull", "--ff-only", "origin", "main")
	return err
}

// CheckoutMain checks out the main branch in repoRoot.
func CheckoutMain(repoRoot string) error {
	_, err := run(repoRoot, "-C", repoRoot, "checkout", "main")
	return err
}

// InferIssue is the exported wrapper around the internal helper.
func InferIssue(branch string) string {
	return inferIssue(branch)
}
