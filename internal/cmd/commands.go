// Package cmd implements the cobra subcommands for agentctl.
package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/arun-gupta/agentctl/internal/adapters"
	"github.com/arun-gupta/agentctl/internal/git"
	"github.com/arun-gupta/agentctl/internal/process"
	"github.com/arun-gupta/agentctl/internal/sdd"
	"github.com/arun-gupta/agentctl/internal/state"
)

// ─── start ────────────────────────────────────────────────────────────────────

// NewStartCmd creates the `start` subcommand.
func NewStartCmd() *cobra.Command {
	var (
		agentName string
		headless  bool
		quiet     bool
		sddName   string
	)
	c := &cobra.Command{
		Use:   "start <issue-number-or-url> [slug]",
		Short: "Provision a worktree for an issue and launch a coding agent",
		Long: `Provision an isolated git worktree for a GitHub issue and launch a
coding agent inside it. By default the agent works directly toward a PR
with no spec-review pause.

The issue argument may be a bare issue number (e.g. 42) or a full GitHub
issue URL (e.g. https://github.com/owner/repo/issues/42). When a URL is
given, agentctl locates or clones the target repository automatically so
you do not need to cd into it first.

Use --sdd <name> to opt into a spec-driven development (SDD) methodology
(e.g. plain, speckit, or a custom methodology).`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			issue := args[0]
			slug := ""
			if len(args) > 1 {
				slug = args[1]
			}
			return runStart(issue, slug, agentName, sddName, headless, quiet)
		},
	}
	c.Flags().StringVar(&agentName, "agent", "claude", "Coding agent adapter to use")
	c.Flags().BoolVar(&headless, "headless", false, "Run agent in background (log -> agent.log)")
	c.Flags().BoolVar(&quiet, "quiet", false, "Suppress agent log output; show spinner/heartbeat only")
	c.Flags().StringVar(&sddName, "sdd", "", "SDD methodology to use (e.g. plain, speckit, or custom); omit to skip SDD")
	return c
}

func runStart(issue, slug, agentName, sddName string, headless, quiet bool) error {
	// Validate the adapter exists before doing any setup work.
	if err := validateAdapter(agentName); err != nil {
		return err
	}

	// Resolve the repo root and issue number.  issue may be a bare number
	// ("42") or a full GitHub issue URL ("https://github.com/owner/repo/issues/42").
	repoRoot, issueNum, ghIssueArg, err := repoRootForIssue(issue)
	if err != nil {
		return err
	}
	parentDir := filepath.Dir(repoRoot)
	repoName := filepath.Base(repoRoot)

	// Derive slug from GitHub issue title if not supplied.
	if slug == "" {
		slug, err = slugFromIssue(ghIssueArg)
		if err != nil {
			return err
		}
		fmt.Printf("Derived slug from issue title: %s\n", slug)
	}

	branch := issueNum + "-" + slug
	wtPath := filepath.Join(parentDir, repoName+"-"+issueNum+"-"+slug)

	// Find a free port in the 3010-3100 range.
	port, err := findFreePort(3010, 3100)
	if err != nil {
		return err
	}

	// Create the worktree.
	if _, statErr := os.Stat(wtPath); statErr == nil {
		return fmt.Errorf("worktree already exists: %s", wtPath)
	}
	if err := git.AddWorktree(repoRoot, wtPath, branch); err != nil {
		return fmt.Errorf("git worktree add: %w", err)
	}

	// Seed .env.local from main repo, then append PORT.
	envLocal := filepath.Join(wtPath, ".env.local")
	mainEnvLocal := filepath.Join(repoRoot, ".env.local")
	if data, readErr := os.ReadFile(mainEnvLocal); readErr == nil {
		// Strip any existing PORT= line.
		var filtered []string
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "PORT=") {
				filtered = append(filtered, line)
			}
		}
		if err := os.WriteFile(envLocal, []byte(strings.Join(filtered, "\n")), 0o600); err != nil {
			return err
		}
		fmt.Printf("Copied .env.local from %s\n", repoRoot)
	} else {
		if err := os.WriteFile(envLocal, nil, 0o600); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "WARNING: %s/.env.local not found — worktree will start without OAuth creds\n", repoRoot)
	}
	portLine := fmt.Sprintf("\nPORT=%d\n", port)
	f, err := os.OpenFile(envLocal, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(portLine)
	f.Close()
	if err != nil {
		return err
	}

	// npm install
	npmInstall := exec.Command("npm", "install", "--silent")
	npmInstall.Dir = wtPath
	npmInstall.Stdout = os.Stdout
	npmInstall.Stderr = os.Stderr
	if err := npmInstall.Run(); err != nil {
		return fmt.Errorf("npm install: %w", err)
	}

	// Start dev server.
	devLog, err := os.Create(filepath.Join(wtPath, "dev.log"))
	if err != nil {
		return err
	}
	devCmd := exec.Command("npm", "run", "dev", "--", "-p", fmt.Sprintf("%d", port))
	devCmd.Dir = wtPath
	devCmd.Stdout = devLog
	devCmd.Stderr = devLog
	if err := devCmd.Start(); err != nil {
		devLog.Close()
		return fmt.Errorf("start dev server: %w", err)
	}
	devPID := fmt.Sprintf("%d", devCmd.Process.Pid)
	fmt.Printf("Dev server: http://localhost:%d (log: %s/dev.log)\n", port, wtPath)

	// Generate session ID.
	sessionID, err := generateUUID()
	if err != nil {
		return fmt.Errorf("generate session ID: %w", err)
	}

	// Write core .agent state file.
	af := state.AgentFile{
		Agent:     agentName,
		SessionID: sessionID,
		DevPID:    devPID,
	}
	if err := state.Write(wtPath, af); err != nil {
		return err
	}

	var kickoff string
	portStr := fmt.Sprintf("%d", port)
	if sddName == "" {
		kickoff = sdd.SkipPrompt(issueNum, portStr)
	} else {
		m, sddErr := sdd.Get(sddName)
		if sddErr != nil {
			return sddErr
		}
		kickoff = m.KickoffPrompt(issueNum, portStr)
	}

	return launchAgent(agentName, wtPath, issueNum, portStr, sessionID, kickoff, headless, quiet)
}

// ─── approve-spec ─────────────────────────────────────────────────────────────

// NewApproveSpecCmd creates the `approve-spec` subcommand.
func NewApproveSpecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "approve-spec <issue>",
		Short: "Release the spec-review pause for a paused headless start",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReleasePausedSession(args[0], "proceed")
		},
	}
}

// ─── revise-spec ──────────────────────────────────────────────────────────────

// NewReviseSpecCmd creates the `revise-spec` subcommand.
func NewReviseSpecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revise-spec <issue> <feedback>",
		Short: "Send non-empty revision feedback to a paused start",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[1]) == "" {
				return fmt.Errorf("revise-spec requires non-empty feedback")
			}
			return runReleasePausedSession(args[0], args[1])
		},
	}
}

func runReleasePausedSession(issue, prompt string) error {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}
	wt, found, err := git.FindWorktreeByIssue(repoRoot, issue)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no worktree found for issue %s", issue)
	}

	af, err := state.Read(wt.Path)
	if err != nil || af.Agent == "" {
		return fmt.Errorf("no .agent file for issue %s; cannot resume non-interactively.\nUse 'cd %s && %s --resume' instead.", issue, wt.Path, af.Agent)
	}

	if err := validateAdapter(af.Agent); err != nil {
		return err
	}

	// Check that a spec exists (paused state reached).
	if !specExists(wt.Path) {
		return fmt.Errorf("spec not yet generated for issue %s; paused state not reached.\nTail %s/agent.log to confirm and retry once the pause is reported.", issue, wt.Path)
	}

	if err := agentResume(af.Agent, wt.Path, af.SessionID, prompt); err != nil {
		return err
	}
	fmt.Printf("Released pause for issue %s; Stage 2 running in background.\n", issue)
	fmt.Printf("Tail: %s/agent.log\n", wt.Path)
	return nil
}

// specExists checks whether a spec.md file exists anywhere under
// <worktreePath>/specs/*/spec.md.
func specExists(wtPath string) bool {
	matches, err := filepath.Glob(filepath.Join(wtPath, "specs", "*", "spec.md"))
	return err == nil && len(matches) > 0
}

// ─── discard ──────────────────────────────────────────────────────────────────

// NewDiscardCmd creates the `discard` subcommand.
func NewDiscardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "discard [issue]",
		Short: "Discard worktree + delete local/remote branch (unrecoverable)",
		Long: `Discard the worktree for an issue and delete the local and remote branches.
This action is NOT recoverable. You will be prompted to type YES to confirm.

If no issue number is given, it is inferred from the current branch when
you are inside a linked worktree.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssueArg("discard", args)
			if err != nil {
				return err
			}
			return runRemoveWorktree(issue)
		},
	}
}

func runRemoveWorktree(issue string) error {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}

	wt, found, err := git.FindWorktreeByIssue(repoRoot, issue)
	var wtPath, branch string
	if err != nil {
		return err
	}
	if found {
		wtPath = wt.Path
		branch = wt.Branch
	}

	// If no registered worktree, try to find a local branch.
	if branch == "" {
		branch, _ = git.FindBranchByIssuePrefix(repoRoot, issue)
	}

	if wtPath == "" && branch == "" {
		fmt.Printf("Nothing to remove: no worktree or branch found for issue %s.\n", issue)
		return nil
	}

	if wtPath == "" {
		fmt.Fprintf(os.Stderr, "note: no registered worktree found for issue %s — will still clean up branches.\n", issue)
	}

	fmt.Fprintf(os.Stderr, "WARNING: This will permanently discard all uncommitted and unpushed work for issue #%s.\n", issue)
	if wtPath != "" {
		fmt.Fprintf(os.Stderr, "         Worktree:      %s\n", wtPath)
	} else {
		fmt.Fprintf(os.Stderr, "         Worktree:      (none registered)\n")
	}
	if branch != "" {
		fmt.Fprintf(os.Stderr, "         Branch:        %s (local + remote will be deleted)\n", branch)
	} else {
		fmt.Fprintf(os.Stderr, "         Branch:        (none found)\n")
	}
	fmt.Fprintf(os.Stderr, "         This action is NOT recoverable.\n")
	fmt.Fprintf(os.Stderr, "Type YES to confirm: ")

	var confirm string
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		confirm = sc.Text()
	}
	if strings.ToLower(strings.TrimSpace(confirm)) != "yes" {
		return fmt.Errorf("aborted")
	}

	// Kill running processes.
	if wtPath != "" {
		af, _ := state.Read(wtPath)
		process.Kill(af.DevPID)
		process.Kill(af.AgentPID)
		if err := git.RemoveWorktree(repoRoot, wtPath); err != nil {
			return fmt.Errorf("git worktree remove: %w", err)
		}
		fmt.Printf("Removed %s\n", wtPath)
	}

	if branch != "" && branch != "HEAD" {
		if git.BranchExists(repoRoot, branch) {
			if err := git.DeleteLocalBranch(repoRoot, branch); err != nil {
				return fmt.Errorf("delete local branch: %w", err)
			}
		} else {
			fmt.Printf("Local branch %s already removed\n", branch)
		}
		msg, err := git.DeleteRemoteBranch(repoRoot, branch)
		if err != nil {
			if strings.Contains(msg, "remote ref does not exist") {
				fmt.Printf("Remote branch %s already removed\n", branch)
			} else {
				fmt.Fprintf(os.Stderr, "WARNING: could not delete remote branch %s\n", branch)
				fmt.Fprintln(os.Stderr, msg)
				fmt.Fprintf(os.Stderr, "Delete the remote manually with:\n  git push origin --delete %s\n", branch)
			}
		} else {
			fmt.Printf("Deleted remote branch origin/%s\n", branch)
		}
	}

	return nil
}

// ─── cleanup-merged ───────────────────────────────────────────────────────────

// NewCleanupMergedCmd creates the `cleanup-merged` subcommand.
func NewCleanupMergedCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup-merged [issue]",
		Short: "Post-merge: pull main, remove worktree, delete local+remote branch",
		Long: `Post-merge cleanup for a specific issue: pull main, remove the worktree,
and delete the local and remote branches.

If no issue number is given, it is inferred from the current branch when
you are inside a linked worktree.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssueArg("cleanup-merged", args)
			if err != nil {
				return err
			}
			return runCleanupMerged(issue)
		},
	}
}

func runCleanupMerged(issue string) error {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}
	return cleanupMerged(repoRoot, issue)
}

func cleanupMerged(repoRoot, issue string) error {
	wt, found, err := git.FindWorktreeByIssue(repoRoot, issue)
	var wtPath, branch string
	wtRegistered := found

	if err != nil {
		return err
	}
	if found {
		wtPath = wt.Path
		branch, err = git.CurrentBranch(wtPath)
		if err != nil || branch == "" || branch == "HEAD" {
			return fmt.Errorf("could not determine branch for %s", wtPath)
		}
	} else {
		// Recovery path: worktree is no longer registered.
		branch, _ = git.FindBranchByIssuePrefix(repoRoot, issue)
		if branch == "" {
			return fmt.Errorf("no worktree or local branch found for issue %s", issue)
		}
		repoName := filepath.Base(repoRoot)
		parentDir := filepath.Dir(repoRoot)
		candidate := filepath.Join(parentDir, repoName+"-"+branch)
		if _, statErr := os.Stat(candidate); statErr == nil {
			fmt.Printf("Detected orphaned worktree dir at %s (not registered with git); recovering.\n", candidate)
			wtPath = candidate
		}
	}

	// Ensure primary worktree is on main.
	currentBranch, err := git.CurrentBranch(repoRoot)
	if err != nil {
		return err
	}
	if currentBranch != "main" {
		fmt.Printf("Primary worktree at %s is on '%s'; checking out main...\n", repoRoot, currentBranch)
		if err := git.CheckoutMain(repoRoot); err != nil {
			return fmt.Errorf("cannot check out main in %s — primary worktree has uncommitted changes or a conflict.\nResolve it manually (commit/stash/revert) and re-run", repoRoot)
		}
	}

	// Verify merge via gh CLI.
	prState, err := ghPRState(repoRoot, branch)
	if err != nil {
		return fmt.Errorf("could not determine PR state for %s.\nIs gh installed and authenticated? If this branch has no PR, use:\n  agentctl discard %s", branch, issue)
	}
	if prState != "MERGED" {
		return fmt.Errorf("PR for %s is %s, not MERGED.\nUse: agentctl discard %s", branch, prState, issue)
	}

	fmt.Printf("Pulling main in %s ...\n", repoRoot)
	if err := git.PullFFOnly(repoRoot); err != nil {
		return err
	}

	if wtPath != "" {
		af, _ := state.Read(wtPath)
		process.Kill(af.DevPID)
		process.Kill(af.AgentPID)

		if wtRegistered {
			if err := git.RemoveWorktree(repoRoot, wtPath); err != nil {
				// Check if already unregistered (partial failure recovery).
				wts, _ := git.LinkedWorktrees(repoRoot)
				stillReg := false
				for _, w := range wts {
					if w.Path == wtPath {
						stillReg = true
						break
					}
				}
				if stillReg {
					return fmt.Errorf("git worktree remove failed and the worktree is still registered; aborting")
				}
				fmt.Printf("git worktree remove left an orphan dir at %s; removing it now.\n", wtPath)
				if err2 := os.RemoveAll(wtPath); err2 != nil {
					return err2
				}
			}
		} else if _, statErr := os.Stat(wtPath); statErr == nil {
			if err := os.RemoveAll(wtPath); err != nil {
				return err
			}
		}
		fmt.Printf("Removed %s\n", wtPath)
	}

	if git.BranchExists(repoRoot, branch) {
		if err := git.DeleteLocalBranch(repoRoot, branch); err != nil {
			return err
		}
	} else {
		fmt.Printf("Local branch %s already removed\n", branch)
	}

	msg, err := git.DeleteRemoteBranch(repoRoot, branch)
	if err != nil {
		if strings.Contains(msg, "remote ref does not exist") {
			fmt.Printf("Remote branch %s already removed\n", branch)
		} else {
			fmt.Fprintf(os.Stderr, "WARNING: could not delete remote branch %s\n", branch)
			fmt.Fprintln(os.Stderr, msg)
			fmt.Fprintf(os.Stderr, "Worktree and local branch were removed; delete the remote manually with:\n  git push origin --delete %s\n", branch)
			return fmt.Errorf("remote branch deletion failed")
		}
	} else {
		fmt.Printf("Deleted remote branch origin/%s\n", branch)
	}

	return nil
}

// ─── cleanup-all-merged ───────────────────────────────────────────────────────

// NewCleanupAllMergedCmd creates the `cleanup-all-merged` subcommand.
func NewCleanupAllMergedCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cleanup-all-merged",
		Short: "Batch sweep: run cleanup-merged on every worktree whose PR is MERGED",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCleanupAllMerged()
		},
	}
}

func runCleanupAllMerged() error {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}

	currentBranch, err := git.CurrentBranch(repoRoot)
	if err != nil {
		return err
	}
	if currentBranch != "main" {
		fmt.Printf("Primary worktree at %s is on '%s'; checking out main...\n", repoRoot, currentBranch)
		if err := git.CheckoutMain(repoRoot); err != nil {
			return fmt.Errorf("cannot check out main in %s — primary worktree has uncommitted changes or a conflict.\nResolve it manually (commit/stash/revert) and re-run", repoRoot)
		}
	}

	wts, err := git.LinkedWorktrees(repoRoot)
	if err != nil {
		return err
	}

	cleaned, skipped, failed := 0, 0, 0
	for _, wt := range wts {
		branch := wt.Branch
		if branch == "" || branch == "HEAD" {
			fmt.Printf("Skipping %s: detached HEAD or cannot determine branch\n", wt.Path)
			skipped++
			continue
		}
		prState, err := ghPRState(repoRoot, branch)
		if err != nil || prState == "" {
			fmt.Printf("Skipping %s: no PR found\n", branch)
			skipped++
			continue
		}
		if prState != "MERGED" {
			fmt.Printf("Skipping %s: PR is %s\n", branch, prState)
			skipped++
			continue
		}
		issue := git.InferIssue(branch)
		if issue == "" {
			fmt.Printf("Skipping %s: no numeric issue prefix in branch name\n", branch)
			skipped++
			continue
		}
		fmt.Printf("--- Cleaning issue %s (%s) ---\n", issue, branch)
		if err := cleanupMerged(repoRoot, issue); err != nil {
			fmt.Fprintf(os.Stderr, "FAILED to clean issue %s (%s): %v\n", issue, branch, err)
			failed++
		} else {
			cleaned++
		}
	}

	fmt.Printf("\n%d merged worktrees cleaned, %d skipped\n", cleaned, skipped)
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "%d cleanup(s) failed\n", failed)
		return fmt.Errorf("%d cleanup(s) failed", failed)
	}
	return nil
}

// ─── status ───────────────────────────────────────────────────────────────────

// NewStatusCmd creates the `status` subcommand.
func NewStatusCmd() *cobra.Command {
	var verbose bool
	c := &cobra.Command{
		Use:     "status",
		Aliases: []string{"list"},
		Short:   "Show status table for all linked worktrees",
		Long: `Print a status table of every linked worktree provisioned by agentctl.

Compact (default):  ISSUE  BRANCH  AGENT  PORT  SPEC  PR
Verbose:            ISSUE  BRANCH  AGENT  PATH  PORT  DEV-PID  AGENT-PID  SPEC  PR  SESSION

Spec states:  no-spec | paused | in-progress | done
PR states:    none | OPEN | MERGED | CLOSED`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(verbose)
		},
	}
	c.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show full table including PATH, PIDs, and SESSION")
	return c
}

func runStatus(verbose bool) error {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}

	wts, err := git.LinkedWorktrees(repoRoot)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if verbose {
		fmt.Fprintln(w, "ISSUE\tBRANCH\tAGENT\tPATH\tPORT\tDEV-PID\tAGENT-PID\tSPEC\tPR\tSESSION")
	} else {
		fmt.Fprintln(w, "ISSUE\tBRANCH\tAGENT\tPORT\tSPEC\tPR")
	}

	for _, wt := range wts {
		issue := wt.Issue
		if issue == "" {
			issue = "?"
		}
		branch := wt.Branch
		if branch == "" {
			branch = "?"
		}

		af, _ := state.Read(wt.Path)

		agentName := dash(af.Agent)

		port := "-"
		if envData, err := os.ReadFile(filepath.Join(wt.Path, ".env.local")); err == nil {
			for _, line := range strings.Split(string(envData), "\n") {
				if p, ok := strings.CutPrefix(line, "PORT="); ok {
					port = strings.TrimSpace(p)
					break
				}
			}
		}

		devPIDStr := pidStatus(af.DevPID)
		agentPIDStr := pidStatus(af.AgentPID)
		specState := computeSpecState(wt.Path, wt.Issue)

		prState := "none"
		if branch != "?" && branch != "HEAD" {
			if ps, err := ghPRState(repoRoot, branch); err == nil && ps != "" {
				prState = ps
			}
		}

		sessionStr := "-"
		if af.SessionID != "" {
			n := 8
			if len(af.SessionID) < n {
				n = len(af.SessionID)
			}
			sessionStr = af.SessionID[:n]
		}

		if verbose {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				issue, branch, agentName, wt.Path, port,
				devPIDStr, agentPIDStr, specState, prState, sessionStr)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				issue, branch, agentName, port, specState, prState)
		}
	}
	return w.Flush()
}

// ─── logs ─────────────────────────────────────────────────────────────────────

// NewLogsCmd creates the `logs` subcommand.
func NewLogsCmd() *cobra.Command {
	var (
		lines    int
		noFollow bool
	)
	c := &cobra.Command{
		Use:   "logs <issue>",
		Short: "Stream the agent log for a headless run",
		Long: `Stream agent.log for the given issue to stdout.

By default the last 50 lines are printed and new output is followed until
Ctrl+C. Use --no-follow to print history and exit immediately.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(args[0], lines, noFollow, os.Stdout)
		},
	}
	c.Flags().IntVar(&lines, "lines", 50, "Lines of history to show before following")
	c.Flags().BoolVar(&noFollow, "no-follow", false, "Print history and exit without following")
	return c
}

// runLogs resolves the worktree for issue and streams its agent.log.
func runLogs(issue string, lines int, noFollow bool, w io.Writer) error {
	wtPath, err := findWorktreePath(issue)
	if err != nil {
		return err
	}
	return streamLog(wtPath, lines, noFollow, w, 10*time.Second)
}

// streamLog is the inner implementation of the logs command.
// logWait controls how long to wait for agent.log to appear; callers should
// pass 10*time.Second in production and a short duration in tests.
func streamLog(wtPath string, lines int, noFollow bool, w io.Writer, logWait time.Duration) error {
	logPath := filepath.Join(wtPath, "agent.log")
	if err := waitForFile(logPath, logWait); err != nil {
		return fmt.Errorf("agent log not found — is the agent running? (looked for %s)", logPath)
	}

	args := []string{"-n", strconv.Itoa(lines)}
	if !noFollow {
		args = append(args, "-F")
	}
	args = append(args, logPath)

	tail := exec.Command("tail", args...)
	tail.Stdout = w
	tail.Stderr = os.Stderr

	if noFollow {
		return tail.Run()
	}

	if err := tail.Start(); err != nil {
		return fmt.Errorf("tail agent.log: %w", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	signal.Stop(sigCh)
	_ = tail.Process.Kill()
	_ = tail.Wait()
	return nil
}

// ─── attach ───────────────────────────────────────────────────────────────────

// NewAttachCmd creates the `attach` subcommand.
func NewAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <issue>",
		Short: "Stream the agent log and exit automatically when the agent finishes",
		Long: `Attach to a running headless agent: stream agent.log to stdout and exit
automatically when the agent process ends.

If the agent has already finished, the last 50 lines of agent.log are printed
and the command exits with "agent has already finished".

Press Ctrl+C to detach without stopping the agent.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wtPath, err := findWorktreePath(args[0])
			if err != nil {
				return err
			}
			return attachLog(wtPath, args[0], os.Stdout, 10*time.Second)
		},
	}
}

// attachLog is the inner implementation of the attach command.
// logWait controls how long to wait for agent.log to appear; callers should
// pass 10*time.Second in production and a short duration in tests.
func attachLog(wtPath, issue string, w io.Writer, logWait time.Duration) error {
	af, err := state.Read(wtPath)
	if err != nil {
		return err
	}
	if af.AgentPID == "" {
		return fmt.Errorf("no agent PID recorded for issue %s — was it started headless?", issue)
	}

	logPath := filepath.Join(wtPath, "agent.log")
	if err := waitForFile(logPath, logWait); err != nil {
		return fmt.Errorf("agent log not found — is the agent running? (looked for %s)", logPath)
	}

	// Agent already finished: print last 50 lines and return.
	if !process.IsAlive(af.AgentPID) {
		tail := exec.Command("tail", "-n", "50", logPath)
		tail.Stdout = w
		tail.Stderr = os.Stderr
		_ = tail.Run()
		fmt.Fprintln(w, "agent has already finished")
		return nil
	}

	// Agent still running: stream log and poll for exit.
	pid, _ := strconv.Atoi(af.AgentPID)

	tail := exec.Command("tail", "-n", "50", "-F", logPath)
	tail.Stdout = w
	tail.Stderr = os.Stderr
	if err := tail.Start(); err != nil {
		return fmt.Errorf("tail agent.log: %w", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	for process.IsAlive(af.AgentPID) {
		select {
		case <-sigCh:
			signal.Stop(sigCh)
			_ = tail.Process.Kill()
			_ = tail.Wait()
			fmt.Fprintf(w, "\nagent still running in background (pid %d)\n", pid)
			return nil
		case <-time.After(500 * time.Millisecond):
		}
	}
	signal.Stop(sigCh)

	time.Sleep(200 * time.Millisecond)
	_ = tail.Process.Kill()
	_ = tail.Wait()
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func pidStatus(pid string) string {
	if pid == "" {
		return "-"
	}
	if process.IsAlive(pid) {
		return pid
	}
	return pid + "(dead)"
}

// computeSpecState derives the SpecKit lifecycle state from filesystem
// artifacts under <wtPath>/specs/<issue>-*/.
// Spec pause state from SpecKit-style artefacts on disk.
func computeSpecState(wtPath, issue string) string {
	if issue == "" {
		return "no-spec"
	}
	specGlob := filepath.Join(wtPath, "specs", issue+"-*", "spec.md")
	specs, err := filepath.Glob(specGlob)
	if err != nil || len(specs) == 0 {
		return "no-spec"
	}
	tasksGlob := filepath.Join(wtPath, "specs", issue+"-*", "tasks.md")
	if tasks, _ := filepath.Glob(tasksGlob); len(tasks) > 0 {
		return "done"
	}
	planGlob := filepath.Join(wtPath, "specs", issue+"-*", "plan.md")
	if plans, _ := filepath.Glob(planGlob); len(plans) > 0 {
		return "in-progress"
	}
	return "paused"
}

// ghPRState calls `gh pr view <branch> --json state -q .state` in repoRoot.
func ghPRState(repoRoot, branch string) (string, error) {
	cmd := exec.Command("gh", "pr", "view", branch, "--json", "state", "-q", ".state")
	cmd.Dir = repoRoot
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// parseIssueURL checks whether arg is a full GitHub issue URL of the form
// https://github.com/<owner>/<repo>/issues/<number>.
// If so it returns the owner, repo name, issue number string, and true.
// Otherwise it returns the original arg as the issue and false (bare number path).
func parseIssueURL(arg string) (owner, repo, issueNum string, ok bool) {
	const prefix = "https://github.com/"
	if !strings.HasPrefix(arg, prefix) {
		return "", "", arg, false
	}
	tail := strings.TrimSuffix(strings.TrimPrefix(arg, prefix), "/")
	parts := strings.Split(tail, "/")
	if len(parts) != 4 || parts[2] != "issues" {
		return "", "", arg, false
	}
	if _, err := strconv.Atoi(parts[3]); err != nil {
		return "", "", arg, false
	}
	return parts[0], parts[1], parts[3], true
}

// matchesGitHubOrigin reports whether the "origin" remote of repoRoot points
// to github.com/<owner>/<repoName>. Both HTTPS and SSH remote URL formats are
// handled, and a trailing ".git" suffix is ignored.
func matchesGitHubOrigin(repoRoot, owner, repoName string) bool {
	u, err := git.OriginURL(repoRoot)
	if err != nil {
		return false
	}
	u = strings.TrimSuffix(u, ".git")
	suffix := owner + "/" + repoName
	return strings.HasSuffix(u, "/"+suffix) || strings.HasSuffix(u, ":"+suffix)
}

// locateOrCloneRepo returns the local git repo root for github.com/<owner>/<repoName>.
// It searches in order:
//  1. The repo that contains the current working directory.
//  2. A sibling directory named <repoName> (i.e. "../<repoName>").
//  3. Clones the repo into "../<repoName>" via `gh repo clone`.
func locateOrCloneRepo(owner, repoName string) (string, error) {
	// 1. Current working directory.
	if root, err := git.RepoRoot(); err == nil && matchesGitHubOrigin(root, owner, repoName) {
		return root, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}

	// 2. Sibling directory.
	sibling := filepath.Join(filepath.Dir(cwd), repoName)
	if info, statErr := os.Stat(sibling); statErr == nil && info.IsDir() {
		if matchesGitHubOrigin(sibling, owner, repoName) {
			return sibling, nil
		}
		return "", fmt.Errorf("directory %s exists but does not match %s/%s", sibling, owner, repoName)
	}

	// 3. Clone via gh repo clone.
	target := filepath.Join(filepath.Dir(cwd), repoName)
	fmt.Fprintf(os.Stdout, "Cloning %s/%s into %s ...\n", owner, repoName, target)
	cloneCmd := exec.Command("gh", "repo", "clone", owner+"/"+repoName, target)
	cloneCmd.Stdout = os.Stdout
	cloneCmd.Stderr = os.Stderr
	if err := cloneCmd.Run(); err != nil {
		return "", fmt.Errorf("gh repo clone %s/%s: %w", owner, repoName, err)
	}
	return target, nil
}

// repoRootForIssue resolves the local git repo root to use, along with the
// bare issue number and the argument to pass to `gh issue view`.
//
// When arg is a bare issue number the repo is inferred from the current
// working directory (existing behaviour). When arg is a full GitHub issue URL
// (https://github.com/<owner>/<repo>/issues/<number>) the target repository is
// located or cloned automatically, so the caller does not need to cd first.
func repoRootForIssue(arg string) (repoRoot, issueNum, ghIssueArg string, err error) {
	owner, repoName, issueNum, isURL := parseIssueURL(arg)
	if !isURL {
		root, err := git.RepoRoot()
		if err != nil {
			return "", "", "", fmt.Errorf("cannot determine repo root: %w", err)
		}
		return root, arg, arg, nil
	}
	root, err := locateOrCloneRepo(owner, repoName)
	if err != nil {
		return "", "", "", err
	}
	// Pass the original URL to gh so it resolves without requiring a
	// matching git remote in the working directory.
	return root, issueNum, arg, nil
}

// slugFromIssue fetches the GitHub issue title and converts it to a slug.
// issueArg may be a bare issue number or a full GitHub issue URL; both are
// accepted by `gh issue view`.
func slugFromIssue(issueArg string) (string, error) {
	cmd := exec.Command("gh", "issue", "view", issueArg, "--json", "title", "-q", ".title")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("could not fetch title for issue %s; pass a slug explicitly", issueArg)
	}
	title := strings.TrimSpace(out.String())
	if title == "" {
		return "", fmt.Errorf("could not fetch title for issue %s; pass a slug explicitly", issueArg)
	}
	slug := titleToSlug(title)
	if slug == "" {
		slug = "work"
	}
	return slug, nil
}

// titleToSlug converts a GitHub issue title to a URL-safe branch slug:
// lowercase, non-alphanum replaced by '-', collapsed and trimmed, max 40 chars.
func titleToSlug(title string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else {
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	return s
}

// findFreePort scans the [lo, hi] range for a port that is not in LISTEN state.
func findFreePort(lo, hi int) (int, error) {
	for p := lo; p <= hi; p++ {
		cmd := exec.Command("lsof", fmt.Sprintf("-iTCP:%d", p), "-sTCP:LISTEN")
		if err := cmd.Run(); err != nil {
			// lsof returns non-zero when no process is listening — port is free.
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port in %d-%d", lo, hi)
}

// generateUUID generates a lowercase UUID v4-style string using uuidgen.
func generateUUID() (string, error) {
	out, err := exec.Command("uuidgen").Output()
	if err != nil {
		return "", fmt.Errorf("uuidgen not found; required for session addressability")
	}
	return strings.ToLower(strings.TrimSpace(string(out))), nil
}

// resolveIssueArg returns the issue number from the positional args or infers
// it from the current branch when inside a linked worktree.
func resolveIssueArg(flag string, args []string) (string, error) {
	if len(args) == 1 && args[0] != "" {
		return args[0], nil
	}
	linked, issue, err := git.IsInsideLinkedWorktree()
	if err != nil {
		return "", fmt.Errorf("usage: agentctl %s <issue>", flag)
	}
	if !linked {
		return "", fmt.Errorf("usage: agentctl %s <issue>", flag)
	}
	if issue == "" {
		branch, _ := git.CurrentBranch("")
		return "", fmt.Errorf("cannot infer issue number from branch %q (expected prefix matching ^[0-9]+-).\nRe-run with an explicit issue number:\n  agentctl %s <issue>", branch, flag)
	}
	return issue, nil
}

// validateAdapter checks that an adapter exists and is loadable.
func validateAdapter(name string) error {
	_, err := adapters.Get(name)
	return err
}

// findWorktreePath resolves the linked worktree path for the given issue number.
func findWorktreePath(issue string) (string, error) {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return "", fmt.Errorf("cannot determine repo root: %w", err)
	}
	wt, found, err := git.FindWorktreeByIssue(repoRoot, issue)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("no worktree found for issue %s — has it been started?", issue)
	}
	return wt.Path, nil
}

// launchAgent starts the coding agent in the background via the named adapter,
// then either returns immediately (headless) or streams agent.log to stdout
// until the agent exits (non-headless). quiet suppresses log lines, showing
// only the spinner/heartbeat.
func launchAgent(adapterName, wtPath, issue, port, sessionID, kickoff string, headless, quiet bool) error {
	ad, err := adapters.Get(adapterName)
	if err != nil {
		return err
	}

	if err := ad.CheckBinary(); err != nil {
		return err
	}

	agentCmd := ad.LaunchCmd(kickoff, sessionID)
	agentCmd.Dir = wtPath

	logPath := filepath.Join(wtPath, "agent.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create agent.log: %w", err)
	}

	// In interactive mode, capture output through a pipe so we can parse
	// stream-json events and write human-readable text to the log file
	// progressively. In headless mode the agent writes directly to the file.
	var pr, pw *os.File
	if !headless {
		pr, pw, err = os.Pipe()
		if err != nil {
			logFile.Close()
			return fmt.Errorf("os.Pipe: %w", err)
		}
		agentCmd.Args = append(agentCmd.Args, "--output-format", "stream-json")
		agentCmd.Stdout = pw
		agentCmd.Stderr = pw
	} else {
		agentCmd.Stdout = logFile
		agentCmd.Stderr = logFile
	}

	detachProcess(agentCmd)

	if err := agentCmd.Start(); err != nil {
		if pw != nil {
			pw.Close()
			pr.Close()
		}
		logFile.Close()
		return fmt.Errorf("agent failed to start: %w", err)
	}

	// convWg tracks the converter goroutine so we can drain all remaining pipe
	// content into the log file before signalling followLog to do its final read.
	var convWg sync.WaitGroup
	if headless {
		// The child process inherits the fd; close our copy.
		logFile.Close()
	} else {
		// Close the write end in the parent; the child has its own copy.
		pw.Close()
		// Convert JSON stream events to readable text written to logFile.
		convWg.Add(1)
		go func() {
			defer convWg.Done()
			defer pr.Close()
			defer logFile.Close()
			sc := bufio.NewScanner(pr)
			sc.Buffer(make([]byte, 512*1024), 512*1024)
			for sc.Scan() {
				if text := extractStreamText(sc.Text()); text != "" {
					fmt.Fprintln(logFile, text)
				}
			}
		}()
	}

	pid := agentCmd.Process.Pid
	// Do NOT call Process.Release(): we need to call Wait() below to properly
	// reap the child. Releasing the handle prevents Wait() from working, and
	// kill(0) polling on a zombie process always returns success — causing the
	// monitor loop to spin forever after the agent exits.

	// Reap the child in a background goroutine and signal exitCh when done.
	// Using Wait() instead of kill-0 polling is the reliable way to detect
	// process exit regardless of session/launchd topology.
	exitCh := make(chan struct{})
	go func() {
		if err := agentCmd.Wait(); err != nil {
			fmt.Fprintf(os.Stderr, "agent exited: %v\n", err)
		}
		close(exitCh)
	}()

	// Record the agent PID in .agent (core fields were already written by runStart).
	if err := state.AppendKey(wtPath, "agent-pid", strconv.Itoa(pid)); err != nil {
		return err
	}

	if headless {
		fmt.Printf("Agent PID %d — log: %s\n", pid, logPath)
		fmt.Printf("Session ID: %s\n", sessionID)
		fmt.Printf("Release the pause with: agentctl approve-spec %s\n", issue)
		return nil
	}

	if err := waitForFile(logPath, 10*time.Second); err != nil {
		return err
	}

	logDone := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		followLog(logPath, os.Stdout, logDone, quiet)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	for {
		select {
		case <-exitCh:
			signal.Stop(sigCh)
			convWg.Wait() // drain remaining pipe → log before the final read
			close(logDone)
			wg.Wait()
			return nil
		case <-sigCh:
			signal.Stop(sigCh)
			close(logDone)
			wg.Wait()
			fmt.Fprintf(os.Stdout, "agent still running in background (pid %d)\n", pid)
			return nil
		}
	}
}

// waitForFile polls until path exists or the timeout elapses.
func waitForFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("%s did not appear within %s", path, timeout)
}

// extractStreamText converts a single claude --output-format stream-json line
// into human-readable text. It extracts assistant text/tool-use blocks and the
// final result. Non-JSON lines are returned as-is (plain-text fallback).
func extractStreamText(line string) string {
	var ev struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				Name string `json:"name"`
			} `json:"content"`
		} `json:"message"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return strings.TrimSpace(line) // not JSON — pass through verbatim
	}
	switch ev.Type {
	case "assistant":
		var sb strings.Builder
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "text":
				if t := strings.TrimSpace(c.Text); t != "" {
					sb.WriteString(t)
					sb.WriteByte('\n')
				}
			case "tool_use":
				fmt.Fprintf(&sb, "[%s]\n", c.Name)
			}
		}
		return strings.TrimRight(sb.String(), "\n")
	case "result":
		return strings.TrimSpace(ev.Result)
	}
	return ""
}

// spinnerFrames are the braille Unicode characters used for the spinner animation.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// followLog reads logPath continuously and writes new content to out.
// While the agent is running (done is not yet closed) it provides feedback:
//   - On a terminal: an in-place spinner with elapsed time, updated every 100 ms.
//   - On a non-terminal (pipe/CI): a "still running" heartbeat line every 30 s.
//
// Clearing the spinner before printing each log line keeps output clean.
// On a TTY the spinner is redrawn on the next 100 ms tick after a log line is
// printed — there is intentionally no immediate redraw to keep the logic simple.
//
// When quiet is true, log lines are suppressed and only the spinner/heartbeat
// is shown. After done is closed, any remaining content is flushed (unless quiet).
// Note: agent-process hang-on-exit (issue #78) is a separate concern and is
// not addressed here; that fix belongs in the process-monitoring loop.
func followLog(logPath string, out io.Writer, done <-chan struct{}, quiet bool) {
	f, err := os.Open(logPath)
	if err != nil {
		fmt.Fprintf(out, "warning: unable to follow log: %v\n", err)
		return
	}
	defer f.Close()

	isTTY := isWriterTerminal(out)
	start := time.Now()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	frameIdx := 0
	spinnerShown := false
	lastHeartbeat := time.Now().Add(-30 * time.Second) // print first heartbeat immediately
	reader := bufio.NewReader(f)

	clearSpinner := func() {
		if isTTY && spinnerShown {
			fmt.Fprint(out, "\r\033[K")
			spinnerShown = false
		}
	}

	drainLines := func() {
		for {
			line, err := reader.ReadString('\n')
			if line != "" && !quiet {
				clearSpinner()
				fmt.Fprint(out, line)
			}
			if errors.Is(err, io.EOF) {
				// ReadString may return a partial line (no trailing '\n') together
				// with io.EOF when the writer hasn't finished the line yet. The
				// partial content is already printed above via `if line != ""`.
				// The next drainLines call will pick up the rest once it is written.
				break
			}
			if err != nil {
				break
			}
		}
	}

	for {
		select {
		case <-done:
			drainLines()
			clearSpinner()
			return
		case <-ticker.C:
			drainLines()
			elapsed := time.Since(start).Truncate(time.Second)
			if isTTY {
				fmt.Fprintf(out, "\r%s agent running... %s", spinnerFrames[frameIdx], elapsed)
				spinnerShown = true
				frameIdx = (frameIdx + 1) % len(spinnerFrames)
			} else if time.Since(lastHeartbeat) >= 30*time.Second {
				fmt.Fprintf(out, "agent running... %s\n", elapsed)
				lastHeartbeat = time.Now()
			}
		}
	}
}

// isWriterTerminal reports whether w is backed by a character device (i.e. a
// terminal). It returns false for pipes, regular files, and non-*os.File writers.
func isWriterTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// agentResume starts the coding agent in resume mode using the named adapter.
func agentResume(adapterName, wtPath, sessionID, prompt string) error {
	ad, err := adapters.Get(adapterName)
	if err != nil {
		return err
	}

	resumeCmd := ad.ResumeCmd(prompt, sessionID)
	resumeCmd.Dir = wtPath

	logFile, err := os.OpenFile(filepath.Join(wtPath, "agent.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open agent.log for append: %w", err)
	}
	resumeCmd.Stdout = logFile
	resumeCmd.Stderr = logFile
	detachProcess(resumeCmd)

	if err := resumeCmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("agent resume failed to start: %w", err)
	}
	logFile.Close()
	// Release our reference to the process handle; the agent runs independently.
	_ = resumeCmd.Process.Release()
	return nil
}
