package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/arun-gupta/agentctl/internal/config"
	"github.com/arun-gupta/agentctl/internal/git"
)

// NewBuildCmd creates the `build` subcommand.
func NewBuildCmd() *cobra.Command {
	var outputPath string
	var agentSuffix string

	c := &cobra.Command{
		Use:   "build <issue>",
		Short: "Compile a binary from an issue worktree or PR branch",
		Long: `Compile a binary from the worktree associated with an issue.

Source resolution order:
  1. Linked worktree exists — build directly from the worktree.
  2. No worktree, but a local branch exists — check out into a temporary
     worktree, build, then remove the temporary worktree.
  3. Neither — error with a clear message.

Build command resolution:
  1. build_cmd in .agentctl.yml (use {output} as the binary path placeholder).
  2. go.mod present → go build -o {output} ./cmd/<first-subdir> (or ./ if none).
  3. package.json with a build script → npm run build.
  4. Makefile with a build target → make build.

The built binary path is printed on success.`,
		Example: `  agentctl build 42
  agentctl build 42 --output ~/bin/agentctl-test`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			issue := args[0]
			if _, _, num, ok := parseIssueURL(issue); ok {
				issue = num
			}
			return runBuild(issue, outputPath, agentSuffix, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVarP(&outputPath, "output", "o", "", `Output path for the compiled binary (default: /tmp/<repo>-<issue>)`)
	c.Flags().StringVar(&agentSuffix, "agent", "", "Agent name to disambiguate when multiple worktrees exist for the same issue")
	return c
}

// runBuild is the core logic for `agentctl build`.
func runBuild(issue, outputPath, agentSuffix string, out io.Writer) error {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}

	// Resolve output path.
	if outputPath == "" {
		repoName := filepath.Base(repoRoot)
		outputPath = filepath.Join(os.TempDir(), repoName+"-"+issue)
	}

	cfg, err := config.Read(repoRoot)
	if err != nil {
		return fmt.Errorf("cannot read config: %w", err)
	}

	// Try worktree first (fast path).
	wtPath, wtErr := findWorktreePath(issue, agentSuffix)
	if wtErr == nil {
		return buildFrom(wtPath, outputPath, cfg.BuildCmd, out)
	}

	// Fall back: look for a local branch.
	branch, branchErr := git.FindBranchByIssuePrefix(repoRoot, issue)
	if branchErr != nil || branch == "" {
		// Attempt to fetch from remote before giving up.
		if fetchErr := git.FetchBranch(repoRoot, issue+"-*"); fetchErr == nil {
			branch, branchErr = git.FindBranchByIssuePrefix(repoRoot, issue)
		}
	}
	if branchErr != nil || branch == "" {
		return fmt.Errorf("no worktree and no branch found for issue %s", issue)
	}

	// Check out into a temporary worktree, build, then clean up.
	tmpPath := filepath.Join(os.TempDir(), "agentctl-build-"+issue)
	if err := git.CheckoutWorktree(repoRoot, tmpPath, branch); err != nil {
		return fmt.Errorf("cannot check out branch %s: %w", branch, err)
	}
	defer func() { _ = git.RemoveWorktree(repoRoot, tmpPath) }()
	return buildFrom(tmpPath, outputPath, cfg.BuildCmd, out)
}

// buildFrom runs the build command inside srcPath and reports the output binary.
func buildFrom(srcPath, outputPath, configBuildCmd string, out io.Writer) error {
	buildCmd, err := resolveBuildCmd(srcPath, configBuildCmd, outputPath)
	if err != nil {
		return err
	}

	c := exec.Command("sh", "-c", buildCmd) //nolint:gosec
	c.Dir = srcPath
	c.Stdout = out
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}
	fmt.Fprintf(out, "Built: %s\n", outputPath)
	return nil
}

// resolveBuildCmd returns the build command to execute.
// configBuildCmd takes highest priority; auto-detection is the fallback.
func resolveBuildCmd(srcPath, configBuildCmd, outputPath string) (string, error) {
	if configBuildCmd != "" {
		return strings.ReplaceAll(configBuildCmd, "{output}", outputPath), nil
	}
	return detectBuildCmd(srcPath, outputPath)
}

// detectBuildCmd infers the build command from project files present in srcPath.
func detectBuildCmd(srcPath, outputPath string) (string, error) {
	// 1. go.mod
	if _, err := os.Stat(filepath.Join(srcPath, "go.mod")); err == nil {
		target, err := goTarget(srcPath)
		if err != nil {
			return "", err
		}
		return "go build -o " + outputPath + " " + target, nil
	}

	// 2. package.json with a build script
	pkgJSON := filepath.Join(srcPath, "package.json")
	if _, err := os.Stat(pkgJSON); err == nil {
		if hasNPMBuildScript(pkgJSON) {
			return "npm run build", nil
		}
	}

	// 3. Makefile with a build target
	makeFile := filepath.Join(srcPath, "Makefile")
	if _, err := os.Stat(makeFile); err == nil {
		if hasMakeTarget(makeFile, "build") {
			return "make build", nil
		}
	}

	return "", fmt.Errorf(
		"cannot detect build command: no go.mod, package.json with build script, or Makefile with build target found in %s",
		srcPath,
	)
}

// goTarget returns the Go build target for the given source directory.
// It prefers the first subdirectory of cmd/, falling back to "./".
func goTarget(srcPath string) (string, error) {
	cmdDir := filepath.Join(srcPath, "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		// No cmd/ directory — build from module root.
		return "./", nil
	}
	for _, e := range entries {
		if e.IsDir() {
			return "./cmd/" + e.Name(), nil
		}
	}
	return "./", nil
}

// hasNPMBuildScript reports whether package.json contains a "build" entry
// under "scripts".
func hasNPMBuildScript(pkgJSON string) bool {
	data, err := os.ReadFile(pkgJSON)
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	_, ok := pkg.Scripts["build"]
	return ok
}

// hasMakeTarget reports whether the Makefile contains a target named target.
func hasMakeTarget(makeFile, target string) bool {
	data, err := os.ReadFile(makeFile)
	if err != nil {
		return false
	}
	prefix := target + ":"
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
