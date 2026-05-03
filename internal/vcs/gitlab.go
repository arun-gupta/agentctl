package vcs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/arun-gupta/agentctl/internal/git"
)

// gitlabProvider implements Provider using the glab CLI.
type gitlabProvider struct{}

// compile-time interface check
var _ Provider = gitlabProvider{}

func (g gitlabProvider) CLI() string   { return "glab" }
func (g gitlabProvider) PRTerm() string { return "MR" }

func (g gitlabProvider) AuthCheck() error {
	if os.Getenv("GITLAB_TOKEN") != "" {
		return nil
	}
	if tok := os.Getenv("GL_TOKEN"); tok != "" {
		os.Setenv("GITLAB_TOKEN", tok)
		return nil
	}
	var stderr bytes.Buffer
	cmd := exec.Command("glab", "auth", "status")
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		return nil
	}
	msg := "GITLAB_TOKEN is not set and glab is not authenticated\n\nSet it in your current shell (or shell profile) and re-run:\n  export GITLAB_TOKEN=<your-token>\n\nBoth GITLAB_TOKEN and GL_TOKEN are accepted. To authenticate via glab, run:\n  glab auth login"
	if glErr := strings.TrimSpace(stderr.String()); glErr != "" {
		msg += "\n\n`glab auth status` reported: " + glErr
	}
	return fmt.Errorf("%s", msg)
}

func (g gitlabProvider) FetchIssueTitle(issueArg string) (string, error) {
	// glab issue view accepts a bare number; for full URLs extract the number first.
	num := issueArg
	if _, _, n, _, ok := ParseIssueURL(issueArg); ok {
		num = n
	}
	cmd := exec.Command("glab", "issue", "view", num, "--output", "json")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("could not fetch title for issue %s; pass a slug explicitly", issueArg)
	}
	var result struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Title == "" {
		return "", fmt.Errorf("could not fetch title for issue %s; pass a slug explicitly", issueArg)
	}
	return result.Title, nil
}

func (g gitlabProvider) IssueURL(repoRoot, issueNum string) string {
	u, err := git.OriginURL(repoRoot)
	if err != nil {
		return ""
	}
	u = strings.TrimSuffix(strings.TrimSpace(u), ".git")
	if strings.HasPrefix(u, "https://gitlab.com/") {
		return u + "/-/issues/" + issueNum
	}
	const sshPrefix = "git@gitlab.com:"
	if strings.HasPrefix(u, sshPrefix) {
		return "https://gitlab.com/" + strings.TrimPrefix(u, sshPrefix) + "/-/issues/" + issueNum
	}
	const sshURLPrefix = "ssh://git@gitlab.com/"
	if strings.HasPrefix(u, sshURLPrefix) {
		return "https://gitlab.com/" + strings.TrimPrefix(u, sshURLPrefix) + "/-/issues/" + issueNum
	}
	return ""
}

// glabMRJSON is the subset of fields returned by `glab mr view --output json`.
type glabMRJSON struct {
	State  string `json:"state"`  // "opened", "closed", "merged"
	IID    int    `json:"iid"`    // MR number within project
	WebURL string `json:"web_url"`
	// merge_status: "can_be_merged", "cannot_be_merged", "unchecked", etc.
	MergeStatus string `json:"merge_status"`
}

// glabStateToGH converts a GitLab MR state string to the GitHub-equivalent
// uppercase state used throughout agentctl ("OPEN", "MERGED", "CLOSED").
func glabStateToGH(state string) string {
	switch strings.ToLower(state) {
	case "opened":
		return "OPEN"
	case "merged":
		return "MERGED"
	case "closed":
		return "CLOSED"
	default:
		return strings.ToUpper(state)
	}
}

// glabMergeStatusToGH converts a GitLab merge_status to the GitHub-equivalent
// mergeable string ("MERGEABLE", "CONFLICTING", "UNKNOWN").
func glabMergeStatusToGH(status string) string {
	switch strings.ToLower(status) {
	case "can_be_merged":
		return "MERGEABLE"
	case "cannot_be_merged":
		return "CONFLICTING"
	default:
		return "UNKNOWN"
	}
}

func (g gitlabProvider) prView(repoRoot, ref string) (*glabMRJSON, error) {
	cmd := exec.Command("glab", "mr", "view", ref, "--output", "json")
	cmd.Dir = repoRoot
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	var mr glabMRJSON
	if err := json.Unmarshal(out.Bytes(), &mr); err != nil {
		snippet := strings.TrimSpace(out.String())
		runes := []rune(snippet)
		if len(runes) > 200 {
			snippet = string(runes[:200]) + "..."
		}
		return nil, fmt.Errorf("unexpected glab output: %w (output: %q)", err, snippet)
	}
	return &mr, nil
}

func (g gitlabProvider) PRForBranch(repoRoot, ref string) (prState string, number int, url string, err error) {
	mr, err := g.prView(repoRoot, ref)
	if err != nil {
		return "", 0, "", err
	}
	return glabStateToGH(mr.State), mr.IID, mr.WebURL, nil
}

func (g gitlabProvider) HasPR(repoRoot, branch string) (bool, error) {
	// List MRs for the branch; --source-branch filters by source branch name.
	cmd := exec.Command("glab", "mr", "list", "--source-branch", branch, "--output", "json")
	cmd.Dir = repoRoot
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("%w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return strings.TrimSpace(out.String()) != "[]", nil
}

func (g gitlabProvider) PRMergeInfo(repoRoot, branch string) (prState, mergeable, reviewDecision string, err error) {
	mr, viewErr := g.prView(repoRoot, branch)
	if viewErr != nil {
		return "", "", "", viewErr
	}
	return glabStateToGH(mr.State), glabMergeStatusToGH(mr.MergeStatus), "", nil
}

func (g gitlabProvider) EditPRBody(repoRoot string, number int, body string) error {
	cmd := exec.Command("glab", "mr", "update", strconv.Itoa(number), "--description", body)
	cmd.Dir = repoRoot
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("glab mr update: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return nil
}

func (g gitlabProvider) MergePR(repoRoot, branch, strategy string) error {
	args := []string{"mr", "merge", branch, "--yes"}
	switch strategy {
	case "squash":
		args = append(args, "--squash")
	case "rebase":
		args = append(args, "--rebase")
	// GitLab's default merge is equivalent to GitHub's --merge; no extra flag needed.
	}
	cmd := exec.Command("glab", args...)
	cmd.Dir = repoRoot
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(out.String()))
	}
	return nil
}

func (g gitlabProvider) Clone(ownerRepo, targetDir string, out io.Writer) error {
	cmd := exec.Command("glab", "repo", "clone", ownerRepo, targetDir)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("glab repo clone %s: %w", ownerRepo, err)
	}
	return nil
}
