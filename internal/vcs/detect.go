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
// override is present.  When there is no origin at all, GitHub is returned
// for backward compatibility with local-only repos.
func Detect(repoRoot string) (Provider, error) {
	// Config file override takes priority.
	if cfg, err := config.Read(repoRoot); err == nil && cfg.VCS.Provider != "" {
		switch strings.ToLower(cfg.VCS.Provider) {
		case "github":
			return githubProvider{server: cfg.VCS.Server}, nil
		case "gitlab":
			return gitlabProvider{server: cfg.VCS.Server}, nil
		default:
			return nil, fmt.Errorf("unknown vcs provider %q in .agentctl.yml; valid values: github, gitlab", cfg.VCS.Provider)
		}
	}

	u, err := git.OriginURL(repoRoot)
	if err != nil {
		// No origin remote; fall back to GitHub for backward compatibility
		// with repos that have no remote (e.g. local-only development).
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
		// Unknown origin with no config override: surface an error instead of
		// silently using gh, which would produce confusing auth/lookup failures.
		return nil, fmt.Errorf("could not detect VCS provider from origin %q; set vcs.provider in .agentctl.yml (valid values: github, gitlab)", u)
	}
}

// MatchesOrigin reports whether the "origin" remote of repoRoot points to
// owner/repoName on any recognised remote for provider p. Both HTTPS and
// SSH URL formats are handled; a trailing ".git" suffix is ignored.
// The provider's host is also checked to prevent cross-provider false positives
// (e.g. a GitLab issue URL matching an adjacent GitHub clone with the same path).
func MatchesOrigin(repoRoot, owner, repoName string, p Provider) bool {
	u, err := git.OriginURL(repoRoot)
	if err != nil {
		return false
	}
	u = strings.TrimSuffix(strings.TrimSpace(u), ".git")
	suffix := owner + "/" + repoName
	if !(strings.HasSuffix(u, "/"+suffix) || strings.HasSuffix(u, ":"+suffix)) {
		return false
	}
	// Also verify the origin URL belongs to this provider's hosting domain.
	return p.MatchesHost(u)
}

// ParseIssueURL parses a full GitHub or GitLab issue URL and returns the
// owner, repo, issue number, and provider name ("github" or "gitlab").
//
// Supported formats:
//   - https://github.com/<owner>/<repo>/issues/<num>
//   - https://gitlab.com/<groups...>/<repo>/-/issues/<num>
//
// GitLab supports nested groups: owner may be "group/subgroup" for a project
// hosted under a GitLab group hierarchy.
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
		// GitLab issue URLs follow the pattern:
		//   <groups...>/<repo>/-/issues/<num>
		// where there must be at least one group and one repo before "/-/",
		// giving a minimum of 5 parts: owner/repo/-/issues/num.
		if len(parts) < 5 {
			return "", "", arg, "", false
		}
		// Locate the "/-/issues/<num>" segment.  We start at index 2 so there
		// are always at least two path components (owner + repo) before the dash.
		dashIdx := -1
		for i := 2; i < len(parts)-2; i++ {
			if parts[i] == "-" && parts[i+1] == "issues" {
				dashIdx = i
				break
			}
		}
		if dashIdx == -1 {
			return "", "", arg, "", false
		}
		numStr := parts[dashIdx+2]
		if _, err := strconv.Atoi(numStr); err != nil {
			return "", "", arg, "", false
		}
		// repo = segment immediately before "/-/"; owner = everything before it.
		repoSeg := parts[dashIdx-1]
		ownerSeg := strings.Join(parts[:dashIdx-1], "/")
		return ownerSeg, repoSeg, numStr, "gitlab", true
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
