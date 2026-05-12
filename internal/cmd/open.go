package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/arun-gupta/agentctl/internal/config"
	"github.com/arun-gupta/agentctl/internal/git"
)

// NewOpenCmd creates the `open` subcommand.
func NewOpenCmd() *cobra.Command {
	var agentSuffix string
	var flagEditor string
	var printPath bool

	c := &cobra.Command{
		Use:   "open <issue>",
		Short: "Open the worktree for an issue in the configured editor",
		Long: `Open the worktree for an issue in the configured editor.

Editor resolution order:
  1. --editor flag
  2. editor key in .agentctl.yml
  3. AGENTCTL_EDITOR env var
  4. VISUAL env var
  5. EDITOR env var
  6. Falls back to printing the worktree path if none are set

Use --path to print the worktree path to stdout without opening an editor.`,
		Example: `  agentctl open 42
  agentctl open 42 --editor code
  agentctl open 42 --editor cursor
  cd $(agentctl open 42 --path)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := args[0]
			if _, _, num, ok := parseIssueURL(arg); ok {
				arg = num
			}
			wtPath, err := findWorktreePath(arg, agentSuffix)
			if err != nil {
				return err
			}

			var editor string
			if !printPath {
				repoRoot, _ := git.RepoRoot()
				cfg, _ := config.Read(repoRoot)
				editor = resolveEditor(flagEditor, cfg)
			}

			return openWorktree(wtPath, editor, printPath, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&agentSuffix, "agent", "", "Agent name to disambiguate when multiple worktrees exist for the same issue")
	c.Flags().StringVar(&flagEditor, "editor", "", "Editor to open the worktree in (overrides config and env vars)")
	c.Flags().BoolVar(&printPath, "path", false, "Print the worktree path to stdout instead of opening an editor")
	return c
}

// resolveEditor returns the editor to use based on the resolution order:
// flagEditor > cfg.Editor > AGENTCTL_EDITOR > VISUAL > EDITOR > ""
func resolveEditor(flagEditor string, cfg *config.AgentctlConfig) string {
	if flagEditor != "" {
		return flagEditor
	}
	if cfg != nil && cfg.Editor != "" {
		return cfg.Editor
	}
	if v := os.Getenv("AGENTCTL_EDITOR"); v != "" {
		return v
	}
	if v := os.Getenv("VISUAL"); v != "" {
		return v
	}
	return os.Getenv("EDITOR")
}

// openWorktree launches editor for wtPath, or prints wtPath if printPath is true
// or no editor is resolved.
func openWorktree(wtPath, editor string, printPath bool, out io.Writer) error {
	if printPath || editor == "" {
		fmt.Fprintln(out, wtPath)
		return nil
	}
	editorCmd := exec.Command(editor, wtPath) //nolint:gosec
	if err := editorCmd.Start(); err != nil {
		return fmt.Errorf("cannot open editor %q: %w", editor, err)
	}
	return nil
}
