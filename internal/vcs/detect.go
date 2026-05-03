package vcs

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/arun-gupta/agentctl/internal/config"
	"github.com/arun-gupta/agentctl/internal/git"
)

// Detect returns the Provider for the repository at repoRoot.
//
// Detection order:
//  1. vcs.provider in .agentctl.yml (explicit override, useful for self-hosted instances)
//  2. git origin URL — github.com variants → GitHub, gitlab.com variants → GitLab
//
// Returns an error when the origin matches neither provider and no config
// override is present.
func Detect(repoRoot string) (Provider, error) {
	// Config file override takes priority.
	if cfg, err := config.Read(repoRoot); err == nil && cfg.VCS.Provider != "" {
		switch strings.ToLower(cfg.VCS.Provider) {
		case "github":
			return githubProvider{}, nil
		case "gitlab":
			return gitlabProvider{}, nil
		default:
			return nil, fmt.Errorf("unknown vcs provider %q in .agentctl.yml; valid values: github, gitlab", cfg.VCS.Provider)
		}
	}

	u, err := git.OriginURL(repoRoot)
	if err != nil {
		// No origin remote; fall back to GitHub for backward compatibility.
		return githubProvider{}, nil
	}
	u = strings.TrimSuffix(strings.TrimSpace(u), ".git")

	switch {
	case strings.HasPrefix(u, "https://github.com/"),
		strings.HasPrefix(u, "git@github.com:"),
		strings.HasPrefix(u, "ssh://git@github.com/"):
		return githubProvider{}, nil
	case strings.HasPrefix(u, "https://gitlab.com/"),
		strings.HasPrefix(u, "git@gitlab.com:"),
		strings.HasPrefix(u, "ssh://git@gitlab.com/"):
		return gitlabProvider{}, nil
	default:
		// Unknown origin — fall back to GitHub for backward compatibility.
		return githubProvider{}, nil
	}
}

// MatchesOrigin reports whether the "origin" remote of repoRoot points to
// owner/repoName on any recognised remote for provider p. Both HTTPS and
// SSH URL formats are handled; a trailing ".git" suffix is ignored.
func MatchesOrigin(repoRoot, owner, repoName string, p Provider) bool {
	u, err := git.OriginURL(repoRoot)
	if err != nil {
		return false
	}
	u = strings.TrimSuffix(u, ".git")
	suffix := owner + "/" + repoName
	return strings.HasSuffix(u, "/"+suffix) || strings.HasSuffix(u, ":"+suffix)
}

// ParseIssueURL parses a full GitHub or GitLab issue URL and returns the
// owner, repo, issue number, and provider name ("github" or "gitlab").
//
// Supported formats:
//   - https://github.com/<owner>/<repo>/issues/<num>
//   - https://gitlab.com/<owner>/<repo>/-/issues/<num>
func ParseIssueURL(arg string) (owner, repo, issueNum, providerName string, ok bool) {
	switch {
	case strings.HasPrefix(arg, "https://github.com/"):
		tail := strings.TrimSuffix(strings.TrimPrefix(arg, "https://github.com/"), "/")
		parts := strings.Split(tail, "/")
		if len(parts) != 4 || parts[2] != "issues" {
			return "", "", arg, "", false
		}
		if _, err := strconv.Atoi(parts[3]); err != nil {
			return "", "", arg, "", false
		}
		return parts[0], parts[1], parts[3], "github", true

	case strings.HasPrefix(arg, "https://gitlab.com/"):
		tail := strings.TrimSuffix(strings.TrimPrefix(arg, "https://gitlab.com/"), "/")
		parts := strings.Split(tail, "/")
		// Expected: owner / repo / - / issues / num  (5 parts)
		if len(parts) != 5 || parts[2] != "-" || parts[3] != "issues" {
			return "", "", arg, "", false
		}
		if _, err := strconv.Atoi(parts[4]); err != nil {
			return "", "", arg, "", false
		}
		return parts[0], parts[1], parts[4], "gitlab", true
	}
	return "", "", arg, "", false
}

// ProviderForName returns the provider implementation for a provider name
// ("github" or "gitlab"). Used when the provider is known from a URL parse
// result before a repoRoot is available.
func ProviderForName(name string) (Provider, error) {
	switch strings.ToLower(name) {
	case "github":
		return githubProvider{}, nil
	case "gitlab":
		return gitlabProvider{}, nil
	default:
		return nil, fmt.Errorf("unknown vcs provider %q; valid values: github, gitlab", name)
	}
}
