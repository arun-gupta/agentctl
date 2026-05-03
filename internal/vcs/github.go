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

// githubProvider implements Provider using the gh CLI.
type githubProvider struct{}

// compile-time interface check
var _ Provider = githubProvider{}

func (g githubProvider) CLI() string  { return "gh" }
func (g githubProvider) PRTerm() string { return "PR" }

func (g githubProvider) AuthCheck() error {
	if os.Getenv("GITHUB_TOKEN") != "" {
		return nil
	}
	if tok := os.Getenv("GH_TOKEN"); tok != "" {
		os.Setenv("GITHUB_TOKEN", tok)
		return nil
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("gh", "auth", "token")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		if tok := strings.TrimSpace(stdout.String()); tok != "" {
			os.Setenv("GITHUB_TOKEN", tok)
			return nil
		}
	}
	msg := "GITHUB_TOKEN is not set\n\nSet it in your current shell (or shell profile) and re-run:\n  export GITHUB_TOKEN=<your-token>\n\nBoth GITHUB_TOKEN and GH_TOKEN are accepted. To obtain a token, run:\n  gh auth token"
	if ghErr := strings.TrimSpace(stderr.String()); ghErr != "" {
		msg += "\n\n`gh auth token` reported: " + ghErr
	}
	return fmt.Errorf("%s", msg)
}

func (g githubProvider) FetchIssueTitle(issueArg string) (string, error) {
	cmd := exec.Command("gh", "issue", "view", issueArg, "--json", "title", "-q", ".title")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("could not fetch title for issue %s; pass a slug explicitly", issueArg)
	}
	title := strings.TrimSpace(out.String())
	if title == "" {
		return "", fmt.Errorf("could not fetch title for issue %s; pass a slug explicitly", issueArg)
	}
	return title, nil
}

func (g githubProvider) IssueURL(repoRoot, issueNum string) string {
	u, err := git.OriginURL(repoRoot)
	if err != nil {
		return ""
	}
	u = strings.TrimSuffix(strings.TrimSpace(u), ".git")
	if strings.HasPrefix(u, "https://github.com/") {
		return u + "/issues/" + issueNum
	}
	const sshPrefix = "git@github.com:"
	if strings.HasPrefix(u, sshPrefix) {
		return "https://github.com/" + strings.TrimPrefix(u, sshPrefix) + "/issues/" + issueNum
	}
	const sshURLPrefix = "ssh://git@github.com/"
	if strings.HasPrefix(u, sshURLPrefix) {
		return "https://github.com/" + strings.TrimPrefix(u, sshURLPrefix) + "/issues/" + issueNum
	}
	return ""
}

func (g githubProvider) PRForBranch(repoRoot, ref string) (prState string, number int, url string, err error) {
	cmd := exec.Command("gh", "pr", "view", ref, "--json", "state,number,url")
	cmd.Dir = repoRoot
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err = cmd.Run(); err != nil {
		return "", 0, "", fmt.Errorf("%w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	var result struct {
		State  string `json:"state"`
		Number int    `json:"number"`
		URL    string `json:"url"`
	}
	if err = json.Unmarshal(out.Bytes(), &result); err != nil {
		snippet := strings.TrimSpace(out.String())
		runes := []rune(snippet)
		if len(runes) > 200 {
			snippet = string(runes[:200]) + "..."
		}
		return "", 0, "", fmt.Errorf("unexpected gh output: %w (output: %q)", err, snippet)
	}
	return result.State, result.Number, result.URL, nil
}

func (g githubProvider) HasPR(repoRoot, branch string) (bool, error) {
	cmd := exec.Command("gh", "pr", "list", "--head", branch, "--state", "all", "--json", "number")
	cmd.Dir = repoRoot
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("%w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return strings.TrimSpace(out.String()) != "[]", nil
}

func (g githubProvider) PRMergeInfo(repoRoot, branch string) (prState, mergeable, reviewDecision string, err error) {
	cmd := exec.Command("gh", "pr", "view", branch,
		"--json", "state,mergeable,reviewDecision",
		"-q", ".state+\" \"+.mergeable+\" \"+.reviewDecision")
	cmd.Dir = repoRoot
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", "", "", fmt.Errorf("%s", strings.TrimSpace(errBuf.String()))
	}
	parts := strings.Fields(strings.TrimSpace(out.String()))
	if len(parts) >= 3 {
		return parts[0], parts[1], parts[2], nil
	}
	if len(parts) >= 2 {
		return parts[0], parts[1], "", nil
	}
	if len(parts) == 1 {
		return parts[0], "", "", nil
	}
	return "", "", "", nil
}

func (g githubProvider) EditPRBody(repoRoot string, number int, body string) error {
	cmd := exec.Command("gh", "pr", "edit", strconv.Itoa(number), "--body", body)
	cmd.Dir = repoRoot
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh pr edit: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return nil
}

func (g githubProvider) MergePR(repoRoot, branch, strategy string) error {
	args := []string{"pr", "merge", branch, "--yes"}
	switch strategy {
	case "squash":
		args = append(args, "--squash")
	case "merge":
		args = append(args, "--merge")
	case "rebase":
		args = append(args, "--rebase")
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = repoRoot
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(out.String()))
	}
	return nil
}

func (g githubProvider) Clone(ownerRepo, targetDir string, out io.Writer) error {
	cmd := exec.Command("gh", "repo", "clone", ownerRepo, targetDir)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh repo clone %s: %w", ownerRepo, err)
	}
	return nil
}
