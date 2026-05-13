// agentctl – Go CLI for provisioning isolated git worktrees per GitHub issue
// and launching coding agents inside each one.
//
// Command surface:
//
//	agentctl agent start    [--agent name] [--headless] [--sdd=name] <issue> [slug]
//	agentctl agent logs     [--lines N] [--no-follow] <issue>
//	agentctl agent attach   <issue>
//	agentctl agent resume   <issue> [feedback]
//	agentctl agent status   [--verbose] [--json]
//	agentctl agent diff     [--stat] [--base branch] [--no-pager] <issue>
//	agentctl worktree cleanup  [issue | --all]
//	agentctl worktree discard  [issue]
//	agentctl worktree merge    [--strategy squash|merge|rebase] [--no-delete] [--dry-run] [issue]
//	agentctl worktree open     [--editor name] [--path] [--agent name] <issue>
//	agentctl dev start      [--quiet] [issue]
//	agentctl build
//	agentctl report         [--json]
//	agentctl info
//	agentctl setup
//
// Backward-compat alias: `agentctl start` → `agentctl agent start`
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/arun-gupta/agentctl/internal/cmd"
)

var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "agentctl",
		Version: version,
		Short:   "Manage per-issue git worktrees and launch coding agents",
		Example: `  # Start work on issue #42 (launches Claude Code in a new worktree)
  agentctl agent start 42

  # Backward-compat alias — agentctl start also works
  agentctl start 42

  # Check status of all active worktrees
  agentctl agent status

  # Clean up after the PR for issue #42 is merged
  agentctl worktree cleanup 42`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Two-level command groups introduced in #293.
	root.AddCommand(
		cmd.NewAgentCmd(),
		cmd.NewWorktreeCmd(),
	)

	// Top-level convenience commands (unchanged).
	root.AddCommand(
		cmd.NewDevCmd(),
		cmd.NewBuildCmd(),
		cmd.NewReportCmd(),
		cmd.NewInfoCmd(version),
		cmd.NewSetupCmd(),
	)

	// Hidden internal/plumbing commands.
	root.AddCommand(
		cmd.NewStreamLogCmd(),
		cmd.NewStreamLogOpenHandsCmd(),
		cmd.NewFinaliseDiagnosticsCmd(),
	)

	// Backward-compat alias: `agentctl start` → `agentctl agent start`.
	startAlias := cmd.NewStartCmd()
	startAlias.Hidden = true
	root.AddCommand(startAlias)

	// Pre-register --version without a short alias so that cobra's lazy
	// InitDefaultVersionFlag does not bind -v (which users expect to mean
	// --verbose).  Cobra still detects the flag and prints version output.
	root.Flags().Bool("version", false, "version for agentctl")

	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
