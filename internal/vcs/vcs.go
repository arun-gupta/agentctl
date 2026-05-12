// Package vcs abstracts VCS-provider-specific CLI calls so the full agentctl
// lifecycle works identically on GitHub and GitLab repositories.
package vcs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// authCheckFromCLI is the shared AuthCheck implementation for all providers.
// It checks primaryEnv, then aliasEnv, then extracts a token via `cli auth token`.
func authCheckFromCLI(primaryEnv, aliasEnv, cli string) error {
	if os.Getenv(primaryEnv) != "" {
		return nil
	}
	if tok := os.Getenv(aliasEnv); tok != "" {
		os.Setenv(primaryEnv, tok)
		return nil
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(cli, "auth", "token")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		if tok := strings.TrimSpace(stdout.String()); tok != "" {
			os.Setenv(primaryEnv, tok)
			return nil
		}
	}
	msg := fmt.Sprintf(
		"%s is not set and %s is not authenticated\n\nSet it in your current shell (or shell profile) and re-run:\n  export %s=<your-token>\n\nBoth %s and %s are accepted. To authenticate via %s, run:\n  %s auth login",
		primaryEnv, cli, primaryEnv, primaryEnv, aliasEnv, cli, cli,
	)
	if cliErr := strings.TrimSpace(stderr.String()); cliErr != "" {
		msg += fmt.Sprintf("\n\n`%s auth token` reported: %s", cli, cliErr)
	}
	return fmt.Errorf("%s", msg)
}

// Provider wraps all provider-specific CLI calls used by agentctl commands.
// Two concrete implementations exist: githubProvider (wraps gh) and
// gitlabProvider (wraps glab). The active provider is resolved once per
// command invocation via Detect.
type Provider interface {
	// FetchIssueTitle returns the title of the given issue. issueArg may be
	// a bare number ("42") or a full issue URL; both are accepted by the
	// underlying CLI.
	FetchIssueTitle(issueArg string) (title string, err error)

	// IssueURL builds the canonical issue URL from the git origin of repoRoot.
	// Returns "" when the origin is not a recognised remote for this provider.
	IssueURL(repoRoot, issueNum string) string

	// PRForBranch returns the state, number, and URL of the PR/MR for ref.
	// ref may be a branch name or a PR/MR number string.
	// All values are zero on error.
	PRForBranch(repoRoot, ref string) (prState string, number int, url string, err error)

	// FetchPRDetails fetches the PR/MR number, description body, and URL for
	// the branch or PR number identified by ref.  Used to add issue-closing
	// keywords to the PR/MR body after the agent opens it.
	FetchPRDetails(repoRoot, ref string) (number int, body, url string, err error)

	// HasPR reports whether any PR/MR (open, closed, or merged) exists for branch.
	// Returns (false, err) on auth/network failures so callers can be conservative.
	HasPR(repoRoot, branch string) (bool, error)

	// PRMergeInfo returns the PR/MR state, mergeability, and review decision.
	PRMergeInfo(repoRoot, branch string) (prState, mergeable, reviewDecision string, err error)

	// EditPRBody updates the body of the PR/MR identified by number in repoRoot.
	EditPRBody(repoRoot string, number int, body string) error

	// MergePR merges the PR/MR for branch using strategy (squash/merge/rebase).
	MergePR(repoRoot, branch, strategy string) error

	// AuthCheck verifies that a valid VCS credential is available in the
	// current environment. It may set token environment variables as a
	// side-effect (e.g. GITHUB_TOKEN, GITLAB_TOKEN).
	AuthCheck() error

	// Clone clones ownerRepo (e.g. "myorg/myrepo") into targetDir.
	Clone(ownerRepo, targetDir string, out io.Writer) error

	// CLI returns the CLI tool name ("gh" or "glab") for use in error messages.
	CLI() string

	// Platform returns the human-readable VCS platform name ("GitHub" or "GitLab").
	Platform() string

	// PRTerm returns the provider-appropriate term for a pull/merge request
	// ("PR" for GitHub, "MR" for GitLab).
	PRTerm() string

	// MatchesHost reports whether originURL belongs to this provider's hosting
	// domain (or the configured self-hosted server).  Used by MatchesOrigin to
	// prevent cross-provider false positives.
	MatchesHost(originURL string) bool
}
