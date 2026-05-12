package vcs

import (
	"os"
	"path/filepath"
	"os/exec"
	"testing"

	"github.com/arun-gupta/agentctl/internal/config"
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

func TestDetect_unknownOrigin_returnsError(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://bitbucket.org/myorg/myrepo.git")
	_, err := Detect(dir)
	if err == nil {
		t.Error("Detect should return an error for an unrecognised origin (no vcs.provider config)")
	}
}

func TestDetect_configOverride_passesServerToProvider(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://bitbucket.org/myorg/myrepo.git")
	// Write .agentctl.yml with provider + server override.
	cfg := &config.AgentctlConfig{
		VCS: config.VCSConfig{
			Provider: "gitlab",
			Server:   "https://gitlab.company.com",
		},
	}
	if err := config.Write(dir, cfg); err != nil {
		t.Fatalf("config.Write: %v", err)
	}
	p, err := Detect(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.CLI() != "glab" {
		t.Errorf("CLI() = %q, want %q", p.CLI(), "glab")
	}
	// Verify the server URL is honoured in MatchesHost.
	if !p.MatchesHost("https://gitlab.company.com/org/repo") {
		t.Error("MatchesHost should return true for self-hosted server URL")
	}
}

func TestDetect_configOverride_unknownProvider_error(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".agentctl.yml"), []byte("vcs:\n  provider: bitbucket\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Detect(dir)
	if err == nil {
		t.Error("Detect should return an error for unknown provider in config")
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

// ─── Platform ────────────────────────────────────────────────────────────────

func TestPlatform_github(t *testing.T) {
	p := githubProvider{}
	if p.Platform() != "GitHub" {
		t.Errorf("Platform() = %q, want %q", p.Platform(), "GitHub")
	}
}

func TestPlatform_gitlab(t *testing.T) {
	p := gitlabProvider{}
	if p.Platform() != "GitLab" {
		t.Errorf("Platform() = %q, want %q", p.Platform(), "GitLab")
	}
}

// ─── ParseIssueURL nested groups ─────────────────────────────────────────────

func TestParseIssueURL_gitlabNestedGroups(t *testing.T) {
	owner, repo, issueNum, prov, ok := ParseIssueURL("https://gitlab.com/group/subgroup/myrepo/-/issues/7")
	if !ok {
		t.Fatal("ParseIssueURL should return ok=true for GitLab nested-group URL")
	}
	if owner != "group/subgroup" {
		t.Errorf("owner = %q, want %q", owner, "group/subgroup")
	}
	if repo != "myrepo" {
		t.Errorf("repo = %q, want %q", repo, "myrepo")
	}
	if issueNum != "7" {
		t.Errorf("issueNum = %q, want %q", issueNum, "7")
	}
	if prov != "gitlab" {
		t.Errorf("provider = %q, want %q", prov, "gitlab")
	}
}

func TestParseIssueURL_gitlabDeeplyNestedGroups(t *testing.T) {
	owner, repo, issueNum, prov, ok := ParseIssueURL("https://gitlab.com/a/b/c/myrepo/-/issues/99")
	if !ok {
		t.Fatal("ParseIssueURL should return ok=true for deeply-nested GitLab URL")
	}
	if owner != "a/b/c" {
		t.Errorf("owner = %q, want %q", owner, "a/b/c")
	}
	if repo != "myrepo" {
		t.Errorf("repo = %q, want %q", repo, "myrepo")
	}
	if issueNum != "99" {
		t.Errorf("issueNum = %q, want %q", issueNum, "99")
	}
	if prov != "gitlab" {
		t.Errorf("provider = %q, want %q", prov, "gitlab")
	}
}

// ─── MatchesOrigin cross-provider ────────────────────────────────────────────

func TestMatchesOrigin_githubURLRejectedForGitLab(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://github.com/myorg/myrepo.git")
	p := gitlabProvider{}
	if MatchesOrigin(dir, "myorg", "myrepo", p) {
		t.Error("expected MatchesOrigin false: GitHub origin should not match GitLab provider")
	}
}

func TestMatchesOrigin_gitlabURLRejectedForGitHub(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://gitlab.com/myorg/myrepo.git")
	p := githubProvider{}
	if MatchesOrigin(dir, "myorg", "myrepo", p) {
		t.Error("expected MatchesOrigin false: GitLab origin should not match GitHub provider")
	}
}

func TestMatchesOrigin_gitlab_https(t *testing.T) {
	dir := initGitRepoWithOrigin(t, "https://gitlab.com/myorg/myrepo.git")
	p := gitlabProvider{}
	if !MatchesOrigin(dir, "myorg", "myrepo", p) {
		t.Error("expected MatchesOrigin true for https GitLab URL")
	}
}

// ─── MatchesHost self-hosted ──────────────────────────────────────────────────

func TestMatchesHost_selfHostedGitLab(t *testing.T) {
	p := gitlabProvider{server: "https://gitlab.company.com"}
	if !p.MatchesHost("https://gitlab.company.com/myorg/myrepo") {
		t.Error("expected MatchesHost true for self-hosted GitLab HTTPS URL")
	}
	if p.MatchesHost("https://gitlab.com/myorg/myrepo") {
		t.Error("expected MatchesHost false for canonical GitLab when server is set to different host")
	}
}

func TestMatchesHost_selfHostedGitHub(t *testing.T) {
	p := githubProvider{server: "https://github.company.com"}
	if !p.MatchesHost("https://github.company.com/myorg/myrepo") {
		t.Error("expected MatchesHost true for self-hosted GitHub Enterprise URL")
	}
	if p.MatchesHost("https://github.com/myorg/myrepo") {
		t.Error("expected MatchesHost false for canonical GitHub when server is set to different host")
	}
}
