package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/arun-gupta/agentctl/internal/adapters"
	"github.com/arun-gupta/agentctl/internal/config"
	"github.com/arun-gupta/agentctl/internal/git"
	"github.com/arun-gupta/agentctl/internal/state"
)

// NewInfoCmd creates the `info` subcommand. version is the agentctl version string.
func NewInfoCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show system diagnostics and configuration",
		Long: `Show system environment and configuration diagnostics.

Prints the agentctl version, OS/arch, prerequisite tools (git, gh),
available coding agents, current configuration, and active worktrees.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, _ := git.RepoRoot()
			return runInfo(cmd.OutOrStdout(), version, repoRoot)
		},
	}
}

func runInfo(out io.Writer, version, repoRoot string) error {
	fmt.Fprintf(out, "agentctl %s\n", version)
	fmt.Fprintf(out, "System: %s (%s)\n", systemOS(), runtime.GOARCH)
	fmt.Fprintln(out)

	// Git
	if gitVer, err := runToolCmd("git", "--version"); err != nil {
		fmt.Fprintln(out, "Git: not found ✗")
	} else {
		ver := strings.TrimPrefix(strings.TrimSpace(gitVer), "git version ")
		fmt.Fprintf(out, "Git: %s ✓\n", ver)
	}

	// GitHub CLI
	if _, err := exec.LookPath("gh"); err != nil {
		fmt.Fprintln(out, "GitHub CLI: not found ✗")
	} else {
		ghOut, _ := runToolCmd("gh", "--version")
		ver := parseGHVersion(ghOut)
		if user := ghAuthUser(); user != "" {
			fmt.Fprintf(out, "GitHub CLI: %s ✓ (authenticated as %s)\n", ver, user)
		} else {
			fmt.Fprintf(out, "GitHub CLI: %s (not authenticated)\n", ver)
		}
	}
	fmt.Fprintln(out)

	// Determine default agent (config overrides built-in default of "claude")
	defaultAgent := "claude"
	if repoRoot != "" {
		if cfg, err := config.Read(repoRoot); err == nil && cfg.DefaultAgent != "" {
			defaultAgent = cfg.DefaultAgent
		}
	}

	// Coding Agents — show ALL known adapters, including those not installed.
	// Adapters whose binary is not on PATH show ✗ with the install hint.
	// Adapters that fail to load (e.g. invalid YAML) show an error indicator
	// rather than being silently omitted.
	fmt.Fprintln(out, "Coding Agents:")
	for _, name := range adapters.List() {
		defaultLabel := ""
		if name == defaultAgent {
			defaultLabel = " (default)"
		}

		adapter, err := adapters.Get(name)
		if err != nil {
			fmt.Fprintf(out, "  ✗ %-12s%s (adapter error)\n", name, defaultLabel)
			continue
		}

		if binErr := adapter.CheckBinary(); binErr != nil {
			if adapter.Install != "" {
				fmt.Fprintf(out, "  ✗ %-12s%s (not installed) — install with: %s\n", name, defaultLabel, adapter.Install)
			} else {
				fmt.Fprintf(out, "  ✗ %-12s%s (not installed)\n", name, defaultLabel)
			}
		} else {
			fmt.Fprintf(out, "  ✓ %-12s%s\n", name, defaultLabel)
		}
	}
	fmt.Fprintln(out)

	// Configuration and Active Worktrees require a repo root.
	if repoRoot == "" {
		return nil
	}

	cfgPath := filepath.Join(repoRoot, config.Filename)
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		fmt.Fprintln(out, "Configuration: no .agentctl.yml found")
	} else {
		cfg, err := config.Read(repoRoot)
		if err != nil {
			fmt.Fprintf(out, "Configuration: error reading .agentctl.yml: %v\n", err)
		} else {
			fmt.Fprintln(out, "Configuration: .agentctl.yml found")
			printConfigFields(out, cfg)
		}
	}
	fmt.Fprintln(out)

	// Active Worktrees — only those managed by agentctl (have a .agent file with an agent name)
	wts, err := git.LinkedWorktrees(repoRoot)
	if err != nil {
		fmt.Fprintln(out, "Active Worktrees: 0")
		return nil
	}
	var managed []git.Worktree
	for _, wt := range wts {
		af, _ := state.Read(wt.Path)
		if af.Agent != "" {
			managed = append(managed, wt)
		}
	}
	if len(managed) == 0 {
		fmt.Fprintln(out, "Active Worktrees: 0")
	} else {
		fmt.Fprintf(out, "Active Worktrees: %d\n", len(managed))
		for _, wt := range managed {
			af, _ := state.Read(wt.Path)
			fmt.Fprintf(out, "  %-30s (%s)\n", wt.Branch, af.Agent)
		}
	}

	return nil
}

// printConfigFields writes non-zero config fields to out, one per line, indented.
func printConfigFields(out io.Writer, cfg *config.AgentctlConfig) {
	if cfg.Editor != "" {
		fmt.Fprintf(out, "  editor: %s\n", cfg.Editor)
	}
	if cfg.DefaultAgent != "" {
		fmt.Fprintf(out, "  default_agent: %s\n", cfg.DefaultAgent)
	}
	if cfg.DevServer != "" {
		fmt.Fprintf(out, "  dev_server: %s\n", cfg.DevServer)
	}
	if cfg.MergeStrategy != "" {
		fmt.Fprintf(out, "  merge_strategy: %s\n", cfg.MergeStrategy)
	}
	if cfg.TestCmd != "" {
		fmt.Fprintf(out, "  test_cmd: %s\n", cfg.TestCmd)
	}
	if cfg.Notify {
		fmt.Fprintln(out, "  notify: true")
	}
	if cfg.VCS.Provider != "" {
		fmt.Fprintf(out, "  vcs.provider: %s\n", cfg.VCS.Provider)
	}
	if cfg.VCS.Server != "" {
		fmt.Fprintf(out, "  vcs.server: %s\n", cfg.VCS.Server)
	}
}

// systemOS returns "GOOS kernel-version" where kernel-version comes from
// `uname -r`. Falls back to just GOOS if uname is unavailable.
func systemOS() string {
	osName := runtime.GOOS
	out, err := runToolCmd("uname", "-r")
	if err != nil {
		return osName
	}
	parts := strings.SplitN(out, ".", 3)
	if len(parts) >= 2 {
		return fmt.Sprintf("%s %s.%s", osName, parts[0], parts[1])
	}
	return fmt.Sprintf("%s %s", osName, out)
}

// parseGHVersion extracts the version number from `gh --version` output.
// "gh version 2.45.0 (2024-01-15)\n..." → "2.45.0"
func parseGHVersion(ghOut string) string {
	if ghOut == "" {
		return ""
	}
	firstLine := strings.SplitN(ghOut, "\n", 2)[0]
	parts := strings.Fields(firstLine)
	if len(parts) >= 3 {
		return parts[2]
	}
	return firstLine
}

// ghAuthUser returns the authenticated GitHub username from `gh auth status`,
// or empty string if not authenticated or gh is not available.
func ghAuthUser() string {
	cmd := exec.Command("gh", "auth", "status") //nolint:gosec
	out, _ := cmd.CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if idx := strings.Index(line, "account "); idx >= 0 {
			parts := strings.Fields(line[idx:])
			if len(parts) >= 2 {
				return parts[1]
			}
		}
	}
	return ""
}

// runToolCmd runs an external command and returns trimmed stdout, or an error.
func runToolCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...) //nolint:gosec
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
