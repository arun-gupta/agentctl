// agentctl – Go CLI for provisioning isolated git worktrees per GitHub issue
// and launching coding agents inside each one.
//
// Command surface (start, approve-spec, …):
//
//	agentctl start [--agent name] [--headless] [--no-sdd] <issue> [slug]
//	agentctl approve-spec  <issue>
//	agentctl revise-spec   <issue> <feedback>
//	agentctl discard       [issue]
//	agentctl cleanup       [issue | --all]
//	agentctl status [--verbose]
//	agentctl logs   [--lines N] [--no-follow] <issue>
//	agentctl attach <issue>
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
		Short:   "Provision isolated git worktrees per issue and launch coding agents",
		Long: `agentctl provisions isolated git worktrees per GitHub issue and launches coding agents inside each one. It supports multiple agent back-ends via a simple adapter registry and supports optional spec-driven development (SDD) methodologies through the start command (for example, agentctl start --sdd <name>).`,
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
		cmd.NewApproveSpecCmd(),
		cmd.NewReviseSpecCmd(),
		cmd.NewDiscardCmd(),
		cmd.NewCleanupCmd(),
		cmd.NewStatusCmd(),
		cmd.NewLogsCmd(),
		cmd.NewAttachCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
