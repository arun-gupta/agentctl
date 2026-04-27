// agentctl – Go CLI for provisioning isolated git worktrees per GitHub issue
// and launching coding agents inside each one.
//
// Command surface:
//
//	agentctl start   [--agent name] [--headless] [--sdd=name] <issue> [slug]
//	agentctl resume  <issue> [feedback]
//	agentctl discard [issue]
//	agentctl cleanup [issue | --all]
//	agentctl status  [--verbose]
//	agentctl logs    [--lines N] [--no-follow] <issue>
//	agentctl attach  <issue>
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/arun-gupta/agentctl/internal/cmd"
)

var version = "dev"

func main() {
	root := &cobra.Command{
		Use:     "agentctl",
		Version: version,
		Short:   "Manage per-issue git worktrees and launch coding agents",
		Example: `  # Start work on issue #42 (launches Claude Code in a new worktree)
  agentctl start 42

  # Check status of all active worktrees
  agentctl status

  # Clean up after the PR for issue #42 is merged
  agentctl cleanup 42`,
		SilenceUsage: true,
	}

	root.AddCommand(
		cmd.NewStartCmd(),
		cmd.NewResumeCmd(),
		cmd.NewDiscardCmd(),
		cmd.NewCleanupCmd(),
		cmd.NewStatusCmd(),
		cmd.NewLogsCmd(),
		cmd.NewAttachCmd(),
	)

	// Pre-register --version without a short alias so that cobra's lazy
	// InitDefaultVersionFlag does not bind -v (which users expect to mean
	// --verbose).  Cobra still detects the flag and prints version output.
	root.Flags().Bool("version", false, "version for agentctl")

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
