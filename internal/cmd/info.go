package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/arun-gupta/agentctl/internal/adapters"
	"github.com/arun-gupta/agentctl/internal/config"
	"github.com/arun-gupta/agentctl/internal/git"
	"github.com/arun-gupta/agentctl/internal/state"
)

// kernelVersionRe extracts "major.minor" from uname output (e.g. "6.6.51-rt50" → groups "6","6").
var kernelVersionRe = regexp.MustCompile(`^(\d+)\.(\d+)`)

// Injected functions — overridden in tests to avoid running real external tools.
var (
	runToolCmdFn = runToolCmd
	lookPathFn   = exec.LookPath
	ghAuthUserFn = ghAuthUser
)

// infoSymbols holds status marker strings used in info output.
type infoSymbols struct {
	check string // installed / found
	cross string // not installed / not found
}

func newSymbols(ascii bool) infoSymbols {
	if ascii {
		return infoSymbols{check: "OK", cross: "X"}
	}
	return infoSymbols{check: "✓", cross: "✗"}
}

// NewInfoCmd creates the `info` subcommand. version is the agentctl version string.
func NewInfoCmd(version string) *cobra.Command {
	var noUnicode bool
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Show system diagnostics and configuration",
		Long: `Show system environment and configuration diagnostics.

Prints the agentctl version, OS/arch, prerequisite tools (git, gh),
available coding agents, current configuration, and active worktrees.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, _ := git.RepoRoot()
			ascii := noUnicode || os.Getenv("AGENTCTL_ASCII") == "1"
			return runInfo(cmd.OutOrStdout(), version, repoRoot, ascii)
		},
	}
	cmd.Flags().BoolVar(&noUnicode, "no-unicode", false, "Use ASCII markers instead of Unicode glyphs (also honoured via AGENTCTL_ASCII=1)")
	return cmd
}

func runInfo(out io.Writer, version, repoRoot string, ascii bool) error {
	sym := newSymbols(ascii)

	fmt.Fprintf(out, "agentctl %s\n", version)
	fmt.Fprintf(out, "System: %s (%s)\n", systemOS(), runtime.GOARCH)
	fmt.Fprintln(out)

	// Git
	if gitVer, err := runToolCmdFn("git", "--version"); err != nil {
		fmt.Fprintf(out, "Git: not found %s\n", sym.cross)
	} else {
		ver := strings.TrimPrefix(strings.TrimSpace(gitVer), "git version ")
		fmt.Fprintf(out, "Git: %s %s\n", ver, sym.check)
	}

	// GitHub CLI
	if _, err := lookPathFn("gh"); err != nil {
		fmt.Fprintf(out, "GitHub CLI: not found %s\n", sym.cross)
	} else {
		ghOut, err := runToolCmdFn("gh", "--version")
		if err != nil {
			fmt.Fprintf(out, "GitHub CLI: error running gh --version: %v\n", err)
		} else {
			ver := parseGHVersion(ghOut)
			if user := ghAuthUserFn(); user != "" {
				fmt.Fprintf(out, "GitHub CLI: %s %s (authenticated as %s)\n", ver, sym.check, user)
			} else {
				fmt.Fprintf(out, "GitHub CLI: %s (not authenticated)\n", ver)
			}
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

	// Coding Agents
	fmt.Fprintln(out, "Coding Agents:")
	for _, name := range adapters.List() {
		adapter, err := adapters.Get(name)
		if err != nil {
			continue
		}
		defaultLabel := ""
		if name == defaultAgent {
			defaultLabel = " (default)"
		}
		if binErr := adapter.CheckBinary(); binErr != nil {
			if adapter.Install != "" {
				fmt.Fprintf(out, "  %s %-12s%s(not installed) — install with: %s\n", sym.cross, name, defaultLabel, adapter.Install)
			} else {
				fmt.Fprintf(out, "  %s %-12s%s(not installed)\n", sym.cross, name, defaultLabel)
			}
		} else {
			fmt.Fprintf(out, "  %s %-12s%s\n", sym.check, name, defaultLabel)
		}
	}
	fmt.Fprintln(out)

	// Configuration and Active Worktrees require a repo root
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

	// Active Worktrees
	wts, err := git.LinkedWorktrees(repoRoot)
	if err != nil || len(wts) == 0 {
		fmt.Fprintln(out, "Active Worktrees: 0")
	} else {
		fmt.Fprintf(out, "Active Worktrees: %d\n", len(wts))
		for _, wt := range wts {
			af, _ := state.Read(wt.Path)
			agentName := af.Agent
			if agentName == "" {
				agentName = "unknown"
			}
			fmt.Fprintf(out, "  %-30s (%s)\n", wt.Branch, agentName)
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
	if cfg.BuildCmd != "" {
		fmt.Fprintf(out, "  build_cmd: %s\n", cfg.BuildCmd)
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

// systemOS returns "GOOS major.minor" where the kernel version comes from
// `uname -r`. It extracts only major.minor via regex (e.g. "6.6.51-rt50" → "6.6").
// Falls back to GOOS alone when uname is unavailable, and to "GOOS raw-output"
// when the output contains no dot-separated version numbers.
func systemOS() string {
	osName := runtime.GOOS
	out, err := runToolCmdFn("uname", "-r")
	if err != nil {
		return osName
	}
	out = strings.TrimSpace(out)
	if m := kernelVersionRe.FindStringSubmatch(out); m != nil {
		return fmt.Sprintf("%s %s.%s", osName, m[1], m[2])
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
	// ["gh", "version", "2.45.0", "(2024-...)"]
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
