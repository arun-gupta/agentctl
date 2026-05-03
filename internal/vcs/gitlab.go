package vcs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/arun-gupta/agentctl/internal/git"
)

// gitlabProvider implements Provider using the glab CLI.
type gitlabProvider struct {
	// server is the base URL for a self-hosted GitLab instance.
	// When empty, gitlab.com is used.
	server string
}

// compile-time interface check
var _ Provider = gitlabProvider{}

func (g gitlabProvider) CLI() string      { return "glab" }
func (g gitlabProvider) PRTerm() string   { return "MR" }
func (g gitlabProvider) Platform() string { return "GitLab" }

// baseURL returns the HTTPS base URL for this provider instance.
func (g gitlabProvider) baseURL() string {
	if g.server != "" {
		return strings.TrimRight(g.server, "/")
	}
	return "https://gitlab.com"
}

// MatchesHost reports whether originURL belongs to the GitLab hosting domain
// (or the configured self-hosted GitLab server).
// When a custom server is configured only that server's URL is accepted; the
// canonical gitlab.com host is not matched to prevent cross-instance confusion.
func (g gitlabProvider) MatchesHost(u string) bool {
	if g.server != "" {
		base := strings.TrimRight(g.server, "/")
		if strings.HasPrefix(u, base+"/") {
			return true
		}
		// Also accept SSH form of the server host.
		host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
		host = strings.SplitN(host, "/", 2)[0]
		return strings.HasPrefix(u, "git@"+host+":") || strings.HasPrefix(u, "ssh://git@"+host+"/")
	}
	return strings.HasPrefix(u, "https://gitlab.com/") ||
		strings.HasPrefix(u, "git@gitlab.com:") ||
		strings.HasPrefix(u, "ssh://git@gitlab.com/")
}

func (g gitlabProvider) AuthCheck() error {
	return authCheckFromCLI("GITLAB_TOKEN", "GL_TOKEN", "glab")
}

func (g gitlabProvider) FetchIssueTitle(issueArg string) (string, error) {
	// glab issue view accepts a bare number with an optional --repo flag.
	// For full URLs, extract the owner/repo and number so the command runs
	// against the correct project regardless of the caller's working directory.
	num := issueArg
	args := []string{"issue", "view", num, "--output", "json"}
	if owner, repo, n, _, ok := ParseIssueURL(issueArg); ok {
		num = n
		args = []string{"issue", "view", num, "--repo", owner + "/" + repo, "--output", "json"}
	}
	cmd := exec.Command("glab", args...)
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
	base := g.baseURL()
	if strings.HasPrefix(u, base+"/") {
		return u + "/-/issues/" + issueNum
	}
	if g.server == "" {
		// Canonical gitlab.com SSH forms.
		const sshPrefix = "git@gitlab.com:"
		if strings.HasPrefix(u, sshPrefix) {
			return "https://gitlab.com/" + strings.TrimPrefix(u, sshPrefix) + "/-/issues/" + issueNum
		}
		const sshURLPrefix = "ssh://git@gitlab.com/"
		if strings.HasPrefix(u, sshURLPrefix) {
			return "https://gitlab.com/" + strings.TrimPrefix(u, sshURLPrefix) + "/-/issues/" + issueNum
		}
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
	Description string `json:"description"` // MR description / body
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
	// glab mr view only accepts an MR number, not a branch name.
	// When ref is not a number, resolve it to an MR via mr list --source-branch.
	if _, err := strconv.Atoi(ref); err != nil {
		return g.prByBranch(repoRoot, ref)
	}
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

// prByBranch looks up the MR for a source branch name across all states.
func (g gitlabProvider) prByBranch(repoRoot, branch string) (*glabMRJSON, error) {
	cmd := exec.Command("glab", "mr", "list", "--source-branch", branch, "--state", "all", "--output", "json")
	cmd.Dir = repoRoot
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	var mrs []glabMRJSON
	if err := json.Unmarshal(out.Bytes(), &mrs); err != nil {
		return nil, fmt.Errorf("unexpected glab output: %w", err)
	}
	if len(mrs) == 0 {
		return nil, fmt.Errorf("no MR found for branch %s", branch)
	}
	return &mrs[0], nil
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

func (g gitlabProvider) FetchPRDetails(repoRoot, ref string) (number int, body, url string, err error) {
	mr, err := g.prView(repoRoot, ref)
	if err != nil {
		return 0, "", "", fmt.Errorf("glab mr view: %w", err)
	}
	return mr.IID, mr.Description, mr.WebURL, nil
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
