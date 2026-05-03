package vcs

import (
	"os/exec"
	"testing"
)

// initGitRepoWithOrigin creates a temp git repo with the given origin URL.
func initGitRepoWithOrigin(t *testing.T, originURL string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"remote", "add", "origin", originURL},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// ─── ParseIssueURL ───────────────────────────────────────────────────────────

func TestParseIssueURL_bareNumber(t *testing.T) {
	owner, repo, issueNum, prov, ok := ParseIssueURL("42")
	if ok {
		t.Error("ParseIssueURL(\"42\") should return ok=false")
	}
	if owner != "" || repo != "" || prov != "" {
		t.Errorf("expected empty owner/repo/provider for bare number, got %q %q %q", owner, repo, prov)
	}
	if issueNum != "42" {
		t.Errorf("issueNum = %q, want %q", issueNum, "42")
	}
}

func TestParseIssueURL_github(t *testing.T) {
	owner, repo, issueNum, prov, ok := ParseIssueURL("https://github.com/myorg/myrepo/issues/99")
	if !ok {
		t.Fatal("ParseIssueURL should return ok=true for a valid GitHub URL")
	}
	if owner != "myorg" {
		t.Errorf("owner = %q, want %q", owner, "myorg")
	}
	if repo != "myrepo" {
		t.Errorf("repo = %q, want %q", repo, "myrepo")
	}
	if issueNum != "99" {
		t.Errorf("issueNum = %q, want %q", issueNum, "99")
	}
	if prov != "github" {
		t.Errorf("provider = %q, want %q", prov, "github")
	}
}

func TestParseIssueURL_githubTrailingSlash(t *testing.T) {
	_, _, issueNum, prov, ok := ParseIssueURL("https://github.com/myorg/myrepo/issues/7/")
	if !ok {
		t.Fatal("trailing slash should be accepted")
	}
	if issueNum != "7" {
		t.Errorf("issueNum = %q, want %q", issueNum, "7")
	}
	if prov != "github" {
		t.Errorf("provider = %q, want %q", prov, "github")
	}
}

func TestParseIssueURL_gitlab(t *testing.T) {
	owner, repo, issueNum, prov, ok := ParseIssueURL("https://gitlab.com/myorg/myrepo/-/issues/42")
	if !ok {
		t.Fatal("ParseIssueURL should return ok=true for a valid GitLab URL")
	}
	if owner != "myorg" {
		t.Errorf("owner = %q, want %q", owner, "myorg")
	}
	if repo != "myrepo" {
		t.Errorf("repo = %q, want %q", repo, "myrepo")
	}
	if issueNum != "42" {
		t.Errorf("issueNum = %q, want %q", issueNum, "42")
	}
	if prov != "gitlab" {
		t.Errorf("provider = %q, want %q", prov, "gitlab")
	}
}

func TestParseIssueURL_gitlabTrailingSlash(t *testing.T) {
	_, _, issueNum, prov, ok := ParseIssueURL("https://gitlab.com/myorg/myrepo/-/issues/7/")
	if !ok {
		t.Fatal("trailing slash should be accepted for GitLab URL")
	}
	if issueNum != "7" {
		t.Errorf("issueNum = %q, want %q", issueNum, "7")
	}
	if prov != "gitlab" {
		t.Errorf("provider = %q, want %q", prov, "gitlab")
	}
}

func TestParseIssueURL_invalidPaths(t *testing.T) {
	cases := []string{
		"https://github.com/myorg/myrepo/pull/42",      // pull request URL
		"https://github.com/myorg/myrepo/issues/",      // missing number
		"https://github.com/myorg/myrepo/issues/abc",   // non-numeric
		"https://github.com/myorg/myrepo",              // no issues path
		"https://gitlab.com/myorg/myrepo/issues/42",    // GitLab without /-/ separator
		"https://gitlab.com/myorg/myrepo/-/issues/abc", // non-numeric
		"https://example.com/owner/repo/issues/1",      // unknown host
	}
	for _, c := range cases {
		_, _, _, _, ok := ParseIssueURL(c)
		if ok {
			t.Errorf("ParseIssueURL(%q) should return ok=false", c)
		}
	}
}

// ─── Detect ──────────────────────────────────────────────────────────────────

func TestDetect_github_https(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://github.com/myorg/myrepo.git")
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.CLI() != "gh" {
		t.Errorf("expected gh CLI for GitHub HTTPS origin, got %q", p.CLI())
	}
}

func TestDetect_github_ssh(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "git@github.com:myorg/myrepo.git")
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.CLI() != "gh" {
		t.Errorf("expected gh CLI for GitHub SSH origin, got %q", p.CLI())
	}
}

func TestDetect_gitlab_https(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://gitlab.com/myorg/myrepo.git")
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.CLI() != "glab" {
		t.Errorf("expected glab CLI for GitLab HTTPS origin, got %q", p.CLI())
	}
	if p.PRTerm() != "MR" {
		t.Errorf("expected PRTerm=MR for GitLab, got %q", p.PRTerm())
	}
}

func TestDetect_gitlab_ssh(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "git@gitlab.com:myorg/myrepo.git")
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.CLI() != "glab" {
		t.Errorf("expected glab CLI for GitLab SSH origin, got %q", p.CLI())
	}
}

func TestDetect_noOrigin_fallbackGitHub(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.CLI() != "gh" {
		t.Errorf("expected gh CLI fallback when no origin, got %q", p.CLI())
	}
}

func TestDetect_unknownOrigin_fallbackGitHub(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://bitbucket.org/myorg/myrepo.git")
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.CLI() != "gh" {
		t.Errorf("expected gh CLI fallback for unknown origin, got %q", p.CLI())
	}
}

// ─── MatchesOrigin ────────────────────────────────────────────────────────────

func TestMatchesOrigin_github_https(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://github.com/myorg/myrepo.git")
	p := githubProvider{}
	if !MatchesOrigin(dir, "myorg", "myrepo", p) {
		t.Error("expected MatchesOrigin true for https GitHub URL")
	}
}

func TestMatchesOrigin_github_ssh(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "git@github.com:myorg/myrepo.git")
	p := githubProvider{}
	if !MatchesOrigin(dir, "myorg", "myrepo", p) {
		t.Error("expected MatchesOrigin true for SSH GitHub URL")
	}
}

func TestMatchesOrigin_wrongOwner(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://github.com/otherorg/myrepo.git")
	p := githubProvider{}
	if MatchesOrigin(dir, "myorg", "myrepo", p) {
		t.Error("expected MatchesOrigin false for wrong owner")
	}
}

// ─── glabStateToGH ───────────────────────────────────────────────────────────

func TestGlabStateToGH(t *testing.T) {
	cases := []struct{ in, want string }{
		{"opened", "OPEN"},
		{"merged", "MERGED"},
		{"closed", "CLOSED"},
		{"OPENED", "OPEN"},
	}
	for _, c := range cases {
		got := glabStateToGH(c.in)
		if got != c.want {
			t.Errorf("glabStateToGH(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
