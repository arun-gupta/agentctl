// Package cmd implements the cobra subcommands for agentctl.
package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/arun-gupta/agentctl/internal/agents"
	"github.com/arun-gupta/agentctl/internal/git"
	"github.com/arun-gupta/agentctl/internal/process"
	"github.com/arun-gupta/agentctl/internal/state"
)

// ─── spawn ────────────────────────────────────────────────────────────────────

// NewSpawnCmd creates the `spawn` subcommand.
func NewSpawnCmd() *cobra.Command {
	var (
		agentName  string
		headless   bool
		noSpeckit  bool
	)
	c := &cobra.Command{
		Use:   "spawn <issue> [slug]",
		Short: "Provision a worktree for an issue and launch a coding agent",
		Long: `Provision an isolated git worktree for a GitHub issue and launch a
coding agent inside it. By default the agent follows the SpecKit
spec-driven development lifecycle with a human-in-the-loop pause.

Use --no-speckit to skip the spec lifecycle and have the agent work
directly toward a PR without a spec-review pause.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			issue := args[0]
			slug := ""
			if len(args) > 1 {
				slug = args[1]
			}
			return runSpawn(issue, slug, agentName, headless, noSpeckit)
		},
	}
	c.Flags().StringVar(&agentName, "agent", "claude", "Coding agent adapter to use")
	c.Flags().BoolVar(&headless, "headless", false, "Run agent in background (log -> agent.log)")
	c.Flags().BoolVar(&noSpeckit, "no-speckit", false, "Skip SpecKit lifecycle; agent opens a PR directly")
	return c
}

func runSpawn(issue, slug, agentName string, headless, noSpeckit bool) error {
	// Validate the adapter exists before doing any setup work.
	if err := validateAdapter(agentName); err != nil {
		return err
	}

	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}
	parentDir := filepath.Dir(repoRoot)
	repoName := filepath.Base(repoRoot)

	// Derive slug from GitHub issue title if not supplied.
	if slug == "" {
		slug, err = slugFromIssue(issue)
		if err != nil {
			return err
		}
		fmt.Printf("Derived slug from issue title: %s\n", slug)
	}

	branch := issue + "-" + slug
	wtPath := filepath.Join(parentDir, repoName+"-"+issue+"-"+slug)

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

	if noSpeckit {
		fmt.Fprintf(os.Stderr, "WARNING: --no-speckit skips the SpecKit lifecycle and the spec-review pause.\n")
		fmt.Fprintf(os.Stderr, "         This run is fully automated with NO human-in-the-loop checkpoint.\n")
		fmt.Fprintf(os.Stderr, "         The agent will make changes and open a PR without spec approval.\n")
	}

	kickoff := buildKickoff(issue, port, noSpeckit)

	return launchAgent(agentName, wtPath, issue, fmt.Sprintf("%d", port), sessionID, kickoff, headless)
}

// ─── approve-spec ─────────────────────────────────────────────────────────────

// NewApproveSpecCmd creates the `approve-spec` subcommand.
func NewApproveSpecCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "approve-spec <issue>",
		Short: "Release the spec-review pause for a paused headless spawn",
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
		Short: "Send non-empty revision feedback to a paused spawn",
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

// slugFromIssue fetches the GitHub issue title and converts it to a slug.
func slugFromIssue(issue string) (string, error) {
	cmd := exec.Command("gh", "issue", "view", issue, "--json", "title", "-q", ".title")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("could not fetch title for issue #%s; pass a slug explicitly", issue)
	}
	title := strings.TrimSpace(out.String())
	if title == "" {
		return "", fmt.Errorf("could not fetch title for issue #%s; pass a slug explicitly", issue)
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

// validateAdapter checks that an embedded adapter script exists.
func validateAdapter(name string) error {
	_, err := agents.Read(name)
	return err
}

// buildKickoff constructs the kickoff prompt for the agent.
func buildKickoff(issue string, port int, noSpeckit bool) string {
	portStr := fmt.Sprintf("%d", port)
	if noSpeckit {
		return fmt.Sprintf(`Work on GitHub issue #%s. Read CLAUDE.md for project conventions. Skip the SpecKit lifecycle — do NOT run /speckit.specify, /speckit.plan, /speckit.tasks, or /speckit.implement. Make the changes directly, then push the branch and open a PR; do not merge.

Dev server is already running on port %s.`, issue, portStr)
	}
	return fmt.Sprintf(`Work on GitHub issue #%s. Follow CLAUDE.md (read constitution, DEVELOPMENT.md, PRODUCT.md). Run the SpecKit lifecycle in two stages with a mandatory human-in-the-loop pause in between:

STAGE 1: Run /speckit.specify. When it completes, report the generated spec file path and STOP. Do NOT proceed to /speckit.plan. Wait for explicit user approval — one of the phrases "proceed", "approved", or "go to plan". If the user replies with spec revisions instead of an approval phrase, update the spec and re-enter the paused state (report the updated spec path and wait again). Only an explicit approval phrase releases the pause.

STAGE 2: After approval, run /speckit.plan, then /speckit.tasks, then /speckit.implement in sequence. When done, push the branch and open a PR; do not merge.

Dev server is already running on port %s.`, issue, portStr)
}

// launchAgent inlines the embedded adapter and calls its agent_launch function.
func launchAgent(adapterName, wtPath, issue, port, sessionID, kickoff string, headless bool) error {
	adapterScript, err := agents.Read(adapterName)
	if err != nil {
		return err
	}

	headlessStr := "0"
	if headless {
		headlessStr = "1"
	}

	script := fmt.Sprintf(`set -euo pipefail
%s
agent_launch %q %q %q %q %q %s
`, adapterScript, wtPath, issue, port, sessionID, kickoff, headlessStr)

	var cmd *exec.Cmd
	if headless {
		cmd = exec.Command("bash", "-c", script)
	} else {
		cmd = exec.Command("bash", "-c", script)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	cmd.Dir = wtPath
	return cmd.Run()
}

// agentResume inlines the embedded adapter and calls its agent_resume function.
func agentResume(adapterName, wtPath, sessionID, prompt string) error {
	adapterScript, err := agents.Read(adapterName)
	if err != nil {
		return err
	}

	script := fmt.Sprintf(`set -euo pipefail
%s
agent_resume %q %q
`, adapterScript, wtPath, prompt)

	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = wtPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
