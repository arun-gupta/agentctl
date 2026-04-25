// agentctl – Go CLI for provisioning isolated git worktrees per GitHub issue
// and launching coding agents inside each one.
//
// Command surface (start, approve-spec, …):
//
//	agentctl start [--agent name] [--headless] [--no-speckit] <issue> [slug]
//	agentctl approve-spec  <issue>
//	agentctl revise-spec   <issue> <feedback>
//	agentctl discard       [issue]
//	agentctl cleanup-merged [issue]
//	agentctl cleanup-all-merged
//	agentctl status [--verbose]
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
		Long: `agentctl provisions isolated git worktrees per GitHub issue and launches
coding agents inside each one. It supports multiple agent back-ends via
a simple adapter registry and follows spec-driven development (SDD) by default.`,
		SilenceUsage: true,
	}

	root.AddCommand(
		cmd.NewStartCmd(),
		cmd.NewApproveSpecCmd(),
		cmd.NewReviseSpecCmd(),
		cmd.NewDiscardCmd(),
		cmd.NewCleanupMergedCmd(),
		cmd.NewCleanupAllMergedCmd(),
		cmd.NewStatusCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
