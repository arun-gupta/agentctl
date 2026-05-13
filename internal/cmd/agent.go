package cmd

import "github.com/spf13/cobra"

// NewAgentCmd creates the `agent` parent command.
func NewAgentCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "agent",
		Short: "Start, monitor, and interact with coding agents",
		Long:  `Commands for provisioning, monitoring, and interacting with coding agents running in git worktrees.`,
	}
	c.AddCommand(
		NewStartCmd(),
		NewLogsCmd(),
		NewAttachCmd(),
		NewResumeCmd(),
		NewStatusCmd(),
		NewDiffCmd(),
	)
	return c
}

// NewWorktreeCmd creates the `worktree` parent command.
func NewWorktreeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "worktree",
		Short: "Manage worktrees (cleanup, discard, merge, open)",
		Long:  `Commands for managing git worktrees: cleaning up, discarding, merging, and opening them.`,
	}
	c.AddCommand(
		NewCleanupCmd(),
		NewDiscardCmd(),
		NewMergeCmd(),
		NewOpenCmd(),
	)
	return c
}
