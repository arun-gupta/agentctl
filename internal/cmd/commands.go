// Package cmd implements the cobra subcommands for agentctl.
package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/arun-gupta/agentctl/internal/adapters"
	"github.com/arun-gupta/agentctl/internal/config"
	"github.com/arun-gupta/agentctl/internal/git"
	"github.com/arun-gupta/agentctl/internal/notify"
	"github.com/arun-gupta/agentctl/internal/process"
	"github.com/arun-gupta/agentctl/internal/sdd"
	"github.com/arun-gupta/agentctl/internal/state"
	"github.com/arun-gupta/agentctl/internal/xdg"
)

// ─── start ────────────────────────────────────────────────────────────────────

// NewStartCmd creates the `start` subcommand.
func NewStartCmd() *cobra.Command {
	var (
		agentName  string
		headless   bool
		quiet      bool
		sddName    string
		sendNotify bool
	)
	c := &cobra.Command{
		Use:   "start <issue-number-or-url>[,<issue>...] [slug]",
		Short: "Start work on a GitHub issue in an isolated worktree",
		Long: `Provision an isolated git worktree for a GitHub issue and launch a
coding agent inside it. By default the agent works directly toward a PR
with no spec-review pause.

The issue argument may be a bare issue number (e.g. 42) or a full GitHub
issue URL (e.g. https://github.com/owner/repo/issues/42). When a URL is
given, agentctl locates or clones the target repository automatically so
you do not need to cd into it first.

Multiple issues may be given as a comma-separated list (e.g. 42,43,44).
In that case all agents are started concurrently in headless mode and the
command returns after all agents have been launched. A [slug] argument is
not allowed in batch mode.

Use --sdd <name> to opt into a spec-driven development (SDD) methodology
(e.g. plain, speckit, or a custom methodology).`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rawIssues := strings.Split(args[0], ",")
			issues := make([]string, 0, len(rawIssues))
			for _, iss := range rawIssues {
				if s := strings.TrimSpace(iss); s != "" {
					issues = append(issues, s)
				}
			}

			if len(issues) == 0 {
				return fmt.Errorf("no valid issue tokens found in %q", args[0])
			}

			slug := ""
			if len(args) > 1 {
				slug = args[1]
			}

			if len(issues) > 1 {
				if slug != "" {
					return fmt.Errorf("[slug] argument is not supported when starting multiple issues")
				}
				return runBatch(issues, agentName, sddName, quiet, sendNotify, startOne, os.Stdout, os.Stderr)
			}

			return startOne(issues[0], slug, agentName, sddName, headless, quiet, sendNotify, os.Stdout)
		},
	}
	c.Flags().StringVar(&agentName, "agent", "claude", "Coding agent adapter to use")
	c.Flags().BoolVar(&headless, "headless", false, "Run agent in background (log -> agent.log)")
	c.Flags().BoolVar(&quiet, "quiet", false, "Suppress agent log output; show spinner/heartbeat only")
	c.Flags().StringVar(&sddName, "sdd", "", "SDD methodology to use (e.g. plain, speckit, or custom); omit to skip SDD")
	c.Flags().BoolVar(&sendNotify, "notify", false, "Send a desktop notification when the headless agent finishes")
	return c
}

// resumeHintFmt is printed after a foreground agent exits so users know how
// to send follow-up feedback. %s is replaced with the issue number.
const resumeHintFmt = "agentctl resume %s [feedback]   # no feedback approves; add feedback to request changes\n"

// kickoffTemplate is the default prompt sent to the agent when no --sdd
// methodology is specified. {issue} and {port} are substituted at call time.
const kickoffTemplate = `Work on GitHub issue #{issue}. Read AGENTS.md or README.md for project conventions if present.
Make the changes directly, push the branch, and open a PR. Do not merge.
You are the coding agent — implement changes using your own file-editing and bash tools.
Do not run agentctl, claude, codex, or any other agent-launcher CLI.
Dev server is running on port {port}.`

// buildKickoff returns the default agent kickoff prompt for a plain
// (non-SDD) run with {issue} and {port} substituted. When port is empty,
// the dev-server line is omitted entirely.
func buildKickoff(issue, port string) string {
	s := strings.ReplaceAll(kickoffTemplate, "{issue}", issue)
	if port == "" {
		var lines []string
		for _, line := range strings.Split(s, "\n") {
			if !strings.Contains(line, "{port}") {
				lines = append(lines, line)
			}
		}
		return strings.TrimRight(strings.Join(lines, "\n"), "\n")
	}
	return strings.ReplaceAll(s, "{port}", port)
}

// startOne provisions a worktree for a single issue and launches the agent.
// It is the per-issue unit used by both single-issue and batch invocations.
func startOne(issue, slug, agentName, sddName string, headless, quiet, sendNotify bool, out io.Writer) error {
	// Validate the adapter exists before doing any setup work.
	if err := validateAdapter(agentName); err != nil {
		return err
	}

	// Resolve the repo root and issue number.  issue may be a bare number
	// ("42") or a full GitHub issue URL ("https://github.com/owner/repo/issues/42").
	repoRoot, issueNum, ghIssueArg, err := repoRootForIssue(issue, out)
	if err != nil {
		return err
	}
	parentDir := filepath.Dir(repoRoot)
	repoName := filepath.Base(repoRoot)

	// Combine --notify flag with the per-repo config setting (either enables it).
	// notify: true in .agentctl.yml is only meaningful in headless mode.
	if !sendNotify {
		if cfg, cfgErr := config.Read(repoRoot); cfgErr == nil && cfg.Notify {
			sendNotify = true
		}
	}

	// Validate methodology-specific prerequisites before any side effects.
	if err := validateSDD(sddName, repoRoot); err != nil {
		return err
	}

	// Derive slug from GitHub issue title if not supplied.
	if slug == "" {
		slug, err = slugFromIssue(ghIssueArg)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Derived slug from issue title: %s\n", slug)
	}

	branch := issueNum + "-" + slug
	wtPath := filepath.Join(parentDir, repoName+"-"+issueNum+"-"+slug)

	// Create the worktree.
	if _, statErr := os.Stat(wtPath); statErr == nil {
		return worktreeExistsError(wtPath, issueNum)
	}
	if err := git.AddWorktree(repoRoot, wtPath, branch); err != nil {
		return fmt.Errorf("git worktree add: %w", err)
	}
	cleanupOnError := true
	defer func() {
		if !cleanupOnError {
			return
		}
		if cleanupErr := cleanupFailedStart(repoRoot, wtPath, branch, ""); cleanupErr != nil {
			fmt.Fprintf(out, "Warning: failed to clean up worktree after start failure (path: %s, branch: %s): %v\n", wtPath, branch, cleanupErr)
		}
	}()

	// Seed .env.local from main repo if present (strips any existing PORT= line).
	if err := seedEnvLocal(filepath.Join(repoRoot, ".env.local"), filepath.Join(wtPath, ".env.local")); err != nil {
		return err
	}

	// Start dev server if the project supports it; returns empty strings when skipped.
	devPID, portStr, err := startDevServer(wtPath, out)
	if err != nil {
		return err
	}

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
		DevPort:   portStr,
		SDD:       sddName,
	}
	if err := state.Write(wtPath, af); err != nil {
		return err
	}

	var kickoff string
	if sddName == "" {
		kickoff = buildKickoff(issueNum, portStr)
	} else {
		m, sddErr := sdd.Get(sddName)
		if sddErr != nil {
			return sddErr
		}
		kickoff = m.KickoffPrompt(issueNum, portStr)
	}

	if err := launchAgent(agentName, wtPath, issueNum, portStr, sessionID, kickoff, sddName, headless, quiet, sendNotify, out); err != nil {
		cleanupOnError = false
		if cleanupErr := cleanupFailedStart(repoRoot, wtPath, branch, devPID); cleanupErr != nil {
			return fmt.Errorf("%w\ncleanup warning: %v", err, cleanupErr)
		}
		return err
	}

	cleanupOnError = false
	return nil
}

// runBatch provisions worktrees and launches agents for multiple issues
// concurrently. Each issue is always started in headless mode. Results are
// collected and printed in the original issue order. If any issue fails the
// remaining issues are still attempted and a combined error is returned.
func runBatch(issues []string, agentName, sddName string, quiet, sendNotify bool,
	fn func(issue, slug, agentName, sddName string, headless, quiet, sendNotify bool, out io.Writer) error,
	out io.Writer, errOut io.Writer) error {

	type batchResult struct {
		issue  string
		output string
		err    error
	}
	results := make([]batchResult, len(issues))

	var wg sync.WaitGroup
	for i, iss := range issues {
		wg.Add(1)
		go func(i int, iss string) {
			defer wg.Done()
			var buf strings.Builder
			err := fn(iss, "", agentName, sddName, true, quiet, sendNotify, &buf)
			results[i] = batchResult{issue: iss, output: buf.String(), err: err}
		}(i, iss)
	}
	wg.Wait()

	var hasErr bool
	for _, r := range results {
		fmt.Fprint(out, r.output)
		if r.err != nil {
			fmt.Fprintf(errOut, "[%s] error: %v\n", r.issue, r.err)
			hasErr = true
		}
	}
	if hasErr {
		return fmt.Errorf("one or more issues failed to start")
	}
	return nil
}

// ─── resume ───────────────────────────────────────────────────────────────────

// resumeAuthorisation is the sentence appended to every resume prompt so the
// agent knows it may execute bash commands without additional human approval.
const resumeAuthorisation = "You are authorised to run bash commands (tests, linters, builds) directly without asking for human approval."

// buildResumePrompt returns the prompt sent to the agent on resume.
//
// When sddName is non-empty (SDD mode):
//   - No feedback: spec approved, proceed with implementation.
//   - With feedback: revise the spec at specPath (or specs/spec.md if specPath
//     is empty) then stop again for re-review; do NOT implement yet.
//
// When sddName is empty (non-SDD mode): pass feedback through unchanged
// (or "proceed" when no feedback).
//
// Either way, the bash-authorisation line is appended.
func buildResumePrompt(feedback, sddName, specPath string) string {
	if sddName != "" {
		if feedback == "" {
			return "The spec is approved. Proceed with implementation. " + resumeAuthorisation
		}
		if specPath == "" {
			specPath = "specs/spec.md"
		}
		return "Revise " + specPath + " to incorporate this feedback: " + feedback +
			"\nAfter updating the spec, stop and wait for human approval before" +
			" proceeding with implementation. Do not implement yet. " + resumeAuthorisation
	}
	if feedback == "" {
		return "proceed " + resumeAuthorisation
	}
	return feedback + " " + resumeAuthorisation
}

// NewResumeCmd creates the `resume` subcommand.
func NewResumeCmd() *cobra.Command {
	var (
		headless   bool
		quiet      bool
		sendNotify bool
	)
	c := &cobra.Command{
		Use:   "resume <issue> [feedback]",
		Short: "Resume a paused spec review: approve or revise",
		Long: `Resume a paused agent session.

In SDD mode (when the worktree was started with --sdd):
  Without feedback: sends "The spec is approved. Proceed with implementation."
    plus a bash-authorisation line; the agent begins implementation.
  With feedback: sends the revision text plus the same bash-authorisation line;
    the agent rewrites the spec and pauses again for re-review.

In non-SDD mode (all other sessions):
  Without feedback: sends "proceed" plus a bash-authorisation line.
  With feedback: sends the feedback text plus a bash-authorisation line.

By default the resumed agent streams its output to the terminal (foreground).
Use --headless to run it in the background and write output to agent.log.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 && strings.TrimSpace(args[1]) == "" {
				return fmt.Errorf("feedback must be non-empty; omit it entirely to approve")
			}
			feedback := ""
			if len(args) == 2 {
				feedback = args[1]
			}
			return runReleasePausedSession(args[0], feedback, headless, quiet, sendNotify)
		},
	}
	c.Flags().BoolVar(&headless, "headless", false, "Run agent in background (log -> agent.log)")
	c.Flags().BoolVar(&quiet, "quiet", false, "Suppress agent log output; show spinner/heartbeat only")
	c.Flags().BoolVar(&sendNotify, "notify", false, "Send a desktop notification when the headless agent finishes")
	return c
}

func runReleasePausedSession(issue, feedback string, headless, quiet, sendNotify bool) error {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}

	// Combine --notify flag with the per-repo config setting (either enables it).
	// notify: true in .agentctl.yml is only meaningful in headless mode.
	if !sendNotify {
		if cfg, cfgErr := config.Read(repoRoot); cfgErr == nil && cfg.Notify {
			sendNotify = true
		}
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

	// For SDD runs, require the spec pause to have been reached before resuming.
	if af.SDD != "" && computeSpecState(wt.Path, issue, af.SDD, af.SDDSet) == "no-spec" {
		return fmt.Errorf("spec not yet generated for issue %s; paused state not reached.\nTail %s/agent.log to confirm and retry once the pause is reported.", issue, wt.Path)
	}

	specPath := findSpecPath(wt.Path, issue)
	prompt := buildResumePrompt(feedback, af.SDD, specPath)
	return agentResume(af.Agent, wt.Path, issue, af.SessionID, prompt, headless, quiet, sendNotify)
}

// ─── discard ──────────────────────────────────────────────────────────────────

// NewDiscardCmd creates the `discard` subcommand.
func NewDiscardCmd() *cobra.Command {
	var stale bool
	c := &cobra.Command{
		Use:   "discard [issue]",
		Short: "Permanently delete a worktree and its local/remote branches",
		Long: `Discard the worktree for an issue and delete the local and remote branches.
This action is NOT recoverable. You will be prompted to type YES to confirm.

If no issue number is given, it is inferred from the current branch when
you are inside a linked worktree.

Use --stale to discard all worktrees that have no running agent and no PR.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if stale {
				if len(args) > 0 {
					return fmt.Errorf("--stale and an issue number are mutually exclusive")
				}
				return runDiscardStale()
			}
			issue, err := resolveIssueArg("discard", args)
			if err != nil {
				return err
			}
			return runRemoveWorktree(issue)
		},
	}
	c.Flags().BoolVar(&stale, "stale", false, "Discard all worktrees with no running agent and no PR")
	return c
}

// isAgentRunning returns true when the agent PID recorded in wtPath is alive.
//
// If the agent state cannot be read, treat the worktree conservatively as having
// a running agent so stale-discard does not remove it based on unreadable or
// corrupt state.
func isAgentRunning(wtPath string) bool {
	af, err := state.Read(wtPath)
	if err != nil {
		return true
	}
	return af.AgentPID != "" && process.IsAlive(af.AgentPID)
}

// isStaleWorktree returns true when no agent is running and no PR of any state
// exists for the branch. Returns false conservatively when PR status cannot be
// reliably determined (e.g. auth or network failures) to avoid discarding
// potentially active worktrees.
func isStaleWorktree(repoRoot, branch, wtPath string) bool {
	if isAgentRunning(wtPath) {
		return false
	}
	hasPR, err := ghHasPR(repoRoot, branch)
	if err != nil {
		return false // conservative: can't confirm no PR, skip
	}
	return !hasPR
}

func runDiscardStale() error {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}

	wts, err := git.LinkedWorktrees(repoRoot)
	if err != nil {
		return err
	}

	type staleEntry struct {
		issue  string
		branch string
		wtPath string
	}
	var stale []staleEntry
	for _, wt := range wts {
		branch := wt.Branch
		if branch == "" || branch == "HEAD" {
			continue
		}
		if isStaleWorktree(repoRoot, branch, wt.Path) {
			stale = append(stale, staleEntry{
				issue:  git.InferIssue(branch),
				branch: branch,
				wtPath: wt.Path,
			})
		}
	}

	if len(stale) == 0 {
		fmt.Println("No stale worktrees found.")
		return nil
	}

	fmt.Fprintf(os.Stderr, "WARNING: The following stale worktrees will be permanently discarded:\n\n")
	for _, e := range stale {
		fmt.Fprintf(os.Stderr, "  issue #%s  branch %s\n", e.issue, e.branch)
		fmt.Fprintf(os.Stderr, "             path   %s\n", e.wtPath)
	}
	fmt.Fprintf(os.Stderr, "\nThis action is NOT recoverable.\n")
	fmt.Fprintf(os.Stderr, "Type YES to confirm: ")

	var confirm string
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		confirm = sc.Text()
	}
	if strings.ToLower(strings.TrimSpace(confirm)) != "yes" {
		return fmt.Errorf("aborted")
	}

	discardStaleEntry := func(wtPath, branch string) error {
		if wtPath != "" {
			af, _ := state.Read(wtPath)
			process.Kill(af.DevPID)
			process.Kill(af.AgentPID)
			if err := git.RemoveWorktree(repoRoot, wtPath); err != nil {
				return fmt.Errorf("remove worktree %s: %w", wtPath, err)
			}
			fmt.Printf("Removed %s\n", wtPath)
		}

		if branch != "" && branch != "HEAD" {
			if git.BranchExists(repoRoot, branch) {
				if err := git.DeleteLocalBranch(repoRoot, branch); err != nil {
					return fmt.Errorf("delete local branch %s: %w", branch, err)
				}
			} else {
				fmt.Printf("Local branch %s already removed\n", branch)
			}

			msg, err := git.DeleteRemoteBranch(repoRoot, branch)
			if err != nil {
				if strings.Contains(msg, "remote ref does not exist") {
					fmt.Printf("Remote branch %s already removed\n", branch)
				} else {
					return fmt.Errorf("delete remote branch %s: %s\nDelete the remote manually with:\n  git push origin --delete %s", branch, strings.TrimSpace(msg), branch)
				}
			} else {
				fmt.Printf("Deleted remote branch origin/%s\n", branch)
			}
		}

		return nil
	}

	discarded := 0
	for _, e := range stale {
		if err := discardStaleEntry(e.wtPath, e.branch); err != nil {
			return err
		}
		discarded++
	}

	fmt.Printf("\nDiscarded %d stale worktree(s)\n", discarded)
	return nil
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
		if err := removeBranchRefs(repoRoot, branch); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: could not delete remote branch %s\n", branch)
			fmt.Fprintf(os.Stderr, "Delete the remote manually with:\n  git push origin --delete %s\n", branch)
		}
	}

	return nil
}

// ─── cleanup-merged ───────────────────────────────────────────────────────────

// NewCleanupMergedCmd creates the `cleanup-merged` subcommand.
// NewCleanupCmd creates the `cleanup` subcommand.
// With --all it sweeps all merged worktrees; otherwise it cleans up a single issue.
func NewCleanupCmd() *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use:   "cleanup [issue]",
		Short: "Remove a merged worktree and its branches",
		Long: `Post-merge cleanup: pull main, remove the worktree, and delete the local
and remote branches.

Run without arguments inside a linked worktree to infer the issue number
from the current branch.

Use --all to sweep every linked worktree whose PR is MERGED in one pass.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all {
				if len(args) > 0 {
					return fmt.Errorf("--all and an issue number are mutually exclusive")
				}
				return runCleanupAllMerged()
			}
			issue, err := resolveIssueArg("cleanup", args)
			if err != nil {
				return err
			}
			return runCleanupMerged(issue)
		},
	}
	c.Flags().BoolVar(&all, "all", false, "Clean up all worktrees whose PR is MERGED")
	return c
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

	if err := removeBranchRefs(repoRoot, branch); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not delete remote branch %s\n", branch)
		fmt.Fprintf(os.Stderr, "Delete the remote manually with:\n  git push origin --delete %s\n", branch)
	}

	return nil
}

func removeBranchRefs(repoRoot, branch string) error {
	if branch == "" || branch == "HEAD" {
		return nil
	}
	if git.BranchExists(repoRoot, branch) {
		if err := git.DeleteLocalBranch(repoRoot, branch); err != nil {
			return fmt.Errorf("delete local branch: %w", err)
		}
	}

	msg, err := git.DeleteRemoteBranch(repoRoot, branch)
	if err != nil {
		if strings.Contains(msg, "remote ref does not exist") {
			return nil
		}
		return fmt.Errorf("remote branch deletion failed: %s", strings.TrimSpace(msg))
	}

	fmt.Printf("Deleted remote branch origin/%s\n", branch)
	return nil
}

// ─── cleanup (--all sweep) ────────────────────────────────────────────────────

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

	cleaned, skipped, failed, staleCount := 0, 0, 0, 0
	for _, wt := range wts {
		branch := wt.Branch
		if branch == "" || branch == "HEAD" {
			fmt.Printf("Skipping %s: detached HEAD or cannot determine branch\n", wt.Path)
			skipped++
			continue
		}
		prState, err := ghPRState(repoRoot, branch)
		if err != nil || prState == "" {
			if !isAgentRunning(wt.Path) {
				staleCount++
			}
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
	if staleCount > 0 {
		fmt.Printf("Note: %d stale worktree(s) found with no agent and no PR — run `agentctl discard --stale` to remove them.\n", staleCount)
	}
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
		specState := computeSpecState(wt.Path, wt.Issue, af.SDD, af.SDDSet)

		prState := "none"
		if branch != "?" && branch != "HEAD" {
			if ps, n, err := ghPRInfo(repoRoot, branch); err == nil && ps != "" {
				if n > 0 {
					prState = fmt.Sprintf("#%d %s", n, ps)
				} else {
					prState = ps
				}
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
		Short: "Stream agent.log; follows new output by default",
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
	return streamLog(wtPath, issue, lines, noFollow, w, 10*time.Second)
}

// streamLog is the inner implementation of the logs command.
// logWait controls how long to wait for agent.log to appear; callers should
// pass 10*time.Second in production and a short duration in tests.
func streamLog(wtPath, issue string, lines int, noFollow bool, w io.Writer, logWait time.Duration) error {
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

	// Read agent PID so we can detect process death and notify the user
	// rather than hanging silently when the agent crashes or exits.
	agentPID := ""
	if af, err := state.Read(wtPath); err == nil {
		agentPID = af.AgentPID
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-sigCh:
			signal.Stop(sigCh)
			_ = tail.Process.Kill()
			_ = tail.Wait()
			return nil
		case <-ticker.C:
			// Re-read the PID if we don't have it yet; the agent writes it
			// to .agent after the core fields, so it may not be present when
			// agentctl logs starts.
			if agentPID == "" {
				if af, err := state.Read(wtPath); err == nil {
					agentPID = af.AgentPID
				}
			}
			if agentPID != "" && !process.IsAlive(agentPID) {
				signal.Stop(sigCh)
				time.Sleep(200 * time.Millisecond) // drain any final log writes
				_ = tail.Process.Kill()
				_ = tail.Wait()
				af2, _ := state.Read(wtPath)
				if af2.SDD != "" && findSpecPath(wtPath, issue) != "" {
					fmt.Fprintf(w, "\nSpec ready for review — agentctl resume %s\n", issue)
				} else {
					fmt.Fprintf(w, "\nagent process has exited\n")
				}
				return nil
			}
		}
	}
}

// ─── dev ──────────────────────────────────────────────────────────────────────

// NewDevCmd creates the `dev` subcommand group.
func NewDevCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "dev",
		Short: "Dev server management",
		Long:  `Commands for managing the dev server in a worktree session.`,
	}
	c.AddCommand(NewDevStartCmd())
	return c
}

// NewDevStartCmd creates the `dev start` subcommand.
func NewDevStartCmd() *cobra.Command {
	var quiet bool
	c := &cobra.Command{
		Use:   "start [issue]",
		Short: "Start the dev server in the current worktree",
		Long: `Launch the dev_server command from .agentctl.yml using the port already
recorded in .agent. Does not allocate a new port.

Run without arguments inside a linked worktree to infer the issue number
from the current branch.

By default, streams the dev server's stdout/stderr to the console in
real time. Use --quiet to suppress log streaming and only print the URL.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssueArg("dev start", args)
			if err != nil {
				return err
			}
			wtPath, err := findWorktreePath(issue)
			if err != nil {
				return err
			}
			return runDevStart(wtPath, quiet, os.Stdout)
		},
	}
	c.Flags().BoolVar(&quiet, "quiet", false, "Suppress log streaming; only print the ready URL")
	return c
}

// runDevStart launches the dev server in wtPath using the port already
// recorded in the .agent state file. It updates dev-pid in .agent after
// a successful launch.
func runDevStart(wtPath string, quiet bool, out io.Writer) error {
	cfg, err := config.Read(wtPath)
	if err != nil {
		return fmt.Errorf("reading .agentctl.yml: %w", err)
	}
	if cfg.DevServer == "" {
		return fmt.Errorf("dev_server not set in .agentctl.yml — nothing to start")
	}

	af, err := state.Read(wtPath)
	if err != nil {
		return err
	}
	if af.DevPort == "" {
		return fmt.Errorf("no port recorded in .agent — run `agentctl start` first")
	}

	if process.IsAlive(af.DevPID) {
		return fmt.Errorf("dev server already running (PID %s) — stop that process and clear the recorded dev-pid before restarting", af.DevPID)
	}

	if conflictPID, inUse := isPortInUse(af.DevPort); inUse {
		if conflictPID != "" {
			return fmt.Errorf("port %s already in use by PID %s", af.DevPort, conflictPID)
		}
		return fmt.Errorf("port %s already in use", af.DevPort)
	}

	devPID, err := startDevServerOnPort(wtPath, cfg, af.DevPort, out)
	if err != nil {
		return err
	}

	af.DevPID = devPID
	if err := state.Write(wtPath, af); err != nil {
		return fmt.Errorf("updating .agent: %w", err)
	}

	fmt.Fprintf(out, "Dev server: http://localhost:%s (log: %s/dev.log)\n", af.DevPort, wtPath)

	if cfg.Notify {
		msg := fmt.Sprintf("Dev server: http://localhost:%s", af.DevPort)
		if quiet {
			notify.Send("agentctl", msg)
		} else {
			go notify.Send("agentctl", msg)
		}
	}

	if !quiet {
		return streamDevLog(wtPath, devPID, out)
	}
	return nil
}

// isPortInUse reports whether a process is listening on port.
// Returns the conflicting PID string if detectable.
// It uses lsof when available, distinguishing exit-code-1 ("no listeners") from
// other failures (e.g. lsof absent), and falls back to a cross-platform TCP
// bind probe when lsof cannot be relied upon.
func isPortInUse(port string) (pid string, inUse bool) {
	lsofErr := exec.Command("lsof", fmt.Sprintf("-iTCP:%s", port), "-sTCP:LISTEN").Run()
	if lsofErr == nil {
		// lsof exited 0 — something is listening on the port.
		out, _ := exec.Command("lsof", "-t", fmt.Sprintf("-iTCP:%s", port), "-sTCP:LISTEN").Output() //nolint:gosec
		return strings.TrimSpace(string(out)), true
	}
	// lsof exit code 1 means "no matching processes" (port is free).
	var exitErr *exec.ExitError
	if errors.As(lsofErr, &exitErr) && exitErr.ExitCode() == 1 {
		return "", false
	}
	// lsof unavailable or exited unexpectedly — fall back to a cross-platform TCP bind test.
	ln, err := net.Listen("tcp", net.JoinHostPort("", port))
	if err != nil {
		// Cannot bind — port is already in use.
		return "", true
	}
	_ = ln.Close()
	return "", false
}

// startDevServerOnPort starts the dev server command from cfg using portStr
// (already allocated) rather than finding a new port.
func startDevServerOnPort(dir string, cfg *config.AgentctlConfig, portStr string, out io.Writer) (devPID string, err error) {
	cmdStr := strings.TrimSpace(strings.ReplaceAll(cfg.DevServer, "{port}", portStr))
	if cmdStr == "" {
		return "", fmt.Errorf("dev_server in .agentctl.yml is empty after port substitution")
	}
	devLog, err := os.Create(filepath.Join(dir, "dev.log"))
	if err != nil {
		return "", err
	}
	devCmd := exec.Command("sh", "-c", cmdStr) //nolint:gosec
	devCmd.Dir = dir
	devCmd.Stdout = devLog
	devCmd.Stderr = devLog
	if err := devCmd.Start(); err != nil {
		devLog.Close()
		return "", fmt.Errorf("start dev server: %w", err)
	}
	if err := devLog.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: close dev log: %v\n", err)
	}
	return fmt.Sprintf("%d", devCmd.Process.Pid), nil
}

// streamDevLog tails dev.log in wtPath and blocks until the dev server
// process exits or the user sends SIGINT/SIGTERM.
func streamDevLog(wtPath, devPID string, w io.Writer) error {
	logPath := filepath.Join(wtPath, "dev.log")
	tail := exec.Command("tail", "-n", "0", "-F", logPath)
	tail.Stdout = w
	tail.Stderr = os.Stderr
	if err := tail.Start(); err != nil {
		return fmt.Errorf("tail dev.log: %w", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-sigCh:
			signal.Stop(sigCh)
			_ = tail.Process.Kill()
			_ = tail.Wait()
			return nil
		case <-ticker.C:
			if !process.IsAlive(devPID) {
				signal.Stop(sigCh)
				time.Sleep(200 * time.Millisecond)
				_ = tail.Process.Kill()
				_ = tail.Wait()
				fmt.Fprintln(w, "\ndev server process has exited")
				return nil
			}
		}
	}
}

// ─── attach ───────────────────────────────────────────────────────────────────

// NewAttachCmd creates the `attach` subcommand.
func NewAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <issue>",
		Short: "Follow a running headless agent and exit when it finishes",
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
		if branch, branchErr := git.CurrentBranch(wtPath); branchErr == nil && branch != "" {
			reportPRStatus(w, wtPath, branch, issue)
		}
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
	if branch, branchErr := git.CurrentBranch(wtPath); branchErr == nil && branch != "" {
		reportPRStatus(w, wtPath, branch, issue)
	}
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

// computeSpecState derives the spec lifecycle state from filesystem artifacts.
// sddName is the SDD methodology recorded in .agent (empty when no SDD was requested).
// sddSet is true when the sdd= key was explicitly present in the .agent file:
//   - sddSet=false: legacy worktree written before the sdd key existed — fall back
//     to filesystem heuristics (same as old behaviour).
//   - sddSet=true, sddName="": worktree started without --sdd — return "no-spec".
//   - sddSet=true, sddName!="": SDD was requested — use filesystem heuristics.
//
// It recognises two layouts:
//   - plain-style:   specs/spec.md (flat); always "paused" — no lifecycle files
//   - speckit-style: specs/<issue>-*/spec.md with optional plan.md / tasks.md
func computeSpecState(wtPath, issue, sddName string, sddSet bool) string {
	if issue == "" {
		return "no-spec"
	}
	// New worktree explicitly created without --sdd.
	if sddSet && sddName == "" {
		return "no-spec"
	}
	// Plain-style: flat specs/spec.md with no lifecycle subdirectory.
	if _, err := os.Stat(filepath.Join(wtPath, "specs", "spec.md")); err == nil {
		return "paused"
	}
	// Speckit-style: specs/<issue>-<slug>/ with lifecycle artefacts.
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

// findSpecPath returns the absolute path to spec.md for the given issue inside
// wtPath, or "" if none is found. It checks the plain layout first
// (specs/spec.md), then the speckit layout (specs/<issue>-*/spec.md).
func findSpecPath(wtPath, issue string) string {
	plain := filepath.Join(wtPath, "specs", "spec.md")
	if _, err := os.Stat(plain); err == nil {
		return plain
	}
	matches, _ := filepath.Glob(filepath.Join(wtPath, "specs", issue+"-*", "spec.md"))
	if len(matches) > 0 {
		return matches[0]
	}
	return ""
}

// ghPRInfo calls `gh pr view <branch>` in repoRoot and returns the PR state
// (e.g. "MERGED") and number (e.g. 42). Both are zero-values on error.
func ghPRInfo(repoRoot, branch string) (state string, number int, err error) {
	cmd := exec.Command("gh", "pr", "view", branch, "--json", "state,number", "-q", ".state+\" \"+(.number|tostring)")
	cmd.Dir = repoRoot
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err = cmd.Run(); err != nil {
		return "", 0, fmt.Errorf("%w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	parts := strings.Fields(strings.TrimSpace(out.String()))
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("unexpected gh output: %q", out.String())
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, fmt.Errorf("unexpected gh PR number %q in output %q: %w", parts[1], out.String(), err)
	}
	return parts[0], n, nil
}

// ghHasPR uses `gh pr list --head <branch> --state all` to check whether any
// PR (open, closed, or merged) exists for the branch. It returns (true, nil)
// when at least one PR exists, (false, nil) when none exist, and (false, err)
// when the GH CLI call fails (e.g. auth or network failure) so callers can
// treat the result conservatively.
func ghHasPR(repoRoot, branch string) (bool, error) {
	cmd := exec.Command("gh", "pr", "list", "--head", branch, "--state", "all", "--json", "number")
	cmd.Dir = repoRoot
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("%w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return strings.TrimSpace(out.String()) != "[]", nil
}

// ghPRState is a convenience wrapper that returns only the state string.
func ghPRState(repoRoot, branch string) (string, error) {
	state, _, err := ghPRInfo(repoRoot, branch)
	return state, err
}

type prInfo struct {
	Number int    `json:"number"`
	Body   string `json:"body"`
	URL    string `json:"url"`
}

// linkPRToIssue appends "Closes #<issueNum>" to the open PR for branch,
// unless the body already contains a closing keyword (closes/fixes).
// Returns the PR info (including URL) so callers can display it.
// Silently returns nil, nil when no PR exists.
func linkPRToIssue(dir, branch, issueNum string) (*prInfo, error) {
	cmd := exec.Command("gh", "pr", "view", branch, "--json", "number,body,url")
	cmd.Dir = dir
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		// "no pull request(s) found" means the branch exists but has no PR — not an error.
		// Any other failure (auth, gh not installed, etc.) is a real error.
		errMsg := strings.ToLower(errBuf.String())
		if strings.Contains(errMsg, "no pull request") {
			return nil, nil
		}
		return nil, fmt.Errorf("gh pr view: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}

	var pr prInfo
	if err := json.Unmarshal(out.Bytes(), &pr); err != nil {
		return nil, nil
	}

	lower := strings.ToLower(pr.Body)
	if strings.Contains(lower, "closes #"+issueNum) || strings.Contains(lower, "fixes #"+issueNum) {
		return &pr, nil
	}

	newBody := strings.TrimRight(pr.Body, "\n") + "\n\nCloses #" + issueNum
	editCmd := exec.Command("gh", "pr", "edit", strconv.Itoa(pr.Number), "--body", newBody)
	editCmd.Dir = dir
	var editErr bytes.Buffer
	editCmd.Stderr = &editErr
	if err := editCmd.Run(); err != nil {
		return &pr, fmt.Errorf("gh pr edit: %w: %s", err, strings.TrimSpace(editErr.String()))
	}
	return &pr, nil
}

// reportPRStatus links the PR to the issue and prints the PR URL to w.
// Returns true when a PR was found. Prints "PR: none" when no PR exists,
// "PR: unknown (<reason>)" when the check could not be performed, so callers
// can always tell the user what happened.
func reportPRStatus(w io.Writer, dir, branch, issueNum string) bool {
	// An empty branch means no branch was found or created, so no PR can exist.
	if branch == "" {
		fmt.Fprintln(w, "PR: none")
		return false
	}
	pr, err := linkPRToIssue(dir, branch, issueNum)
	if err != nil {
		fmt.Fprintf(w, "PR: unknown (%v)\n", err)
		return false
	}
	if pr != nil && pr.Number > 0 && pr.URL != "" {
		fmt.Fprintf(w, "PR: #%d  %s\n", pr.Number, pr.URL)
		return true
	}
	fmt.Fprintln(w, "PR: none")
	return false
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
func locateOrCloneRepo(owner, repoName string, out io.Writer) (string, error) {
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
	fmt.Fprintf(out, "Cloning %s/%s into %s ...\n", owner, repoName, target)
	cloneCmd := exec.Command("gh", "repo", "clone", owner+"/"+repoName, target)
	cloneCmd.Stdout = out
	cloneCmd.Stderr = out
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
func repoRootForIssue(arg string, out io.Writer) (repoRoot, issueNum, ghIssueArg string, err error) {
	owner, repoName, issueNum, isURL := parseIssueURL(arg)
	if !isURL {
		root, err := git.RepoRoot()
		if err != nil {
			return "", "", "", fmt.Errorf("cannot determine repo root: %w", err)
		}
		return root, arg, arg, nil
	}
	root, err := locateOrCloneRepo(owner, repoName, out)
	if err != nil {
		return "", "", "", err
	}
	// Pass the original URL to gh so it resolves without requiring a
	// matching git remote in the working directory.
	return root, issueNum, arg, nil
}

// worktreeExistsError returns a descriptive, actionable error for the case
// where the target worktree directory already exists. It reads the .agent
// metadata to distinguish between a still-running agent, a finished agent,
// and a bare worktree with no .agent file.
func worktreeExistsError(wtPath, issueNum string) error {
	af, readErr := state.Read(wtPath)
	if readErr == nil && af.AgentPID != "" && process.IsAlive(af.AgentPID) {
		return fmt.Errorf("Worktree already exists for issue %s — agent is still running.\nWorktree: %s\n\n  agentctl attach %s   to follow its output\n  agentctl discard %s   to delete the worktree and start over", issueNum, wtPath, issueNum, issueNum)
	}
	if readErr == nil && af.AgentPID != "" {
		return fmt.Errorf("Worktree already exists for issue %s — agent has finished.\nWorktree: %s\n\n  agentctl cleanup %s   if the PR is merged\n  agentctl discard %s   to delete the worktree and start over", issueNum, wtPath, issueNum, issueNum)
	}
	return fmt.Errorf("Worktree already exists for issue %s.\nWorktree: %s\n\n  agentctl discard %s   to delete the worktree and start over", issueNum, wtPath, issueNum)
}

func cleanupFailedStart(repoRoot, wtPath, branch, devPID string) error {
	process.Kill(devPID)

	var errs []string
	if _, err := os.Stat(wtPath); err == nil {
		if rmErr := git.RemoveWorktree(repoRoot, wtPath); rmErr != nil {
			wts, listErr := git.LinkedWorktrees(repoRoot)
			registered := false
			if listErr != nil {
				errs = append(errs, rmErr.Error(), listErr.Error())
			} else {
				for _, wt := range wts {
					if wt.Path == wtPath {
						registered = true
						break
					}
				}
				if registered {
					errs = append(errs, rmErr.Error())
				} else if removeErr := os.RemoveAll(wtPath); removeErr != nil {
					errs = append(errs, removeErr.Error())
				}
			}
		}
	}

	if git.BranchExists(repoRoot, branch) {
		if err := git.DeleteLocalBranch(repoRoot, branch); err != nil {
			errs = append(errs, err.Error())
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
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

// seedEnvLocal copies src to dst, stripping any PORT= lines. If src does not
// exist, dst is left untouched.
func seedEnvLocal(src, dst string) error {
	data, err := os.ReadFile(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var filtered []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "PORT=") {
			filtered = append(filtered, line)
		}
	}
	return os.WriteFile(dst, []byte(strings.Join(filtered, "\n")), 0o600)
}

// startDevServer starts a dev server for the project in dir. Detection order:
//  1. .agentctl.yml with dev_server field  → run that command with {port} substituted
//  2. otherwise                            → silently skip, return ("","",nil)
//
// On success the allocated port is written back to .agentctl.yml so it serves
// as the single source of truth for all agentctl repo config.
func startDevServer(dir string, out io.Writer) (devPID, portStr string, err error) {
	cfg, err := config.Read(dir)
	if err != nil {
		return "", "", fmt.Errorf("reading .agentctl.yml: %w", err)
	}

	// Case 1: explicit dev_server in .agentctl.yml
	if cfg.DevServer != "" {
		return startCustomDevServer(dir, cfg, out)
	}

	return "", "", nil
}

func startCustomDevServer(dir string, cfg *config.AgentctlConfig, out io.Writer) (devPID, portStr string, err error) {
	port, err := findFreePort(3010, 3100)
	if err != nil {
		return "", "", err
	}
	portStr = fmt.Sprintf("%d", port)

	cmdStr := strings.TrimSpace(strings.ReplaceAll(cfg.DevServer, "{port}", portStr))
	if cmdStr == "" {
		return "", "", fmt.Errorf("dev_server in .agentctl.yml is empty")
	}
	devLog, err := os.Create(filepath.Join(dir, "dev.log"))
	if err != nil {
		return "", "", err
	}
	devCmd := exec.Command("sh", "-c", cmdStr) //nolint:gosec
	devCmd.Dir = dir
	devCmd.Stdout = devLog
	devCmd.Stderr = devLog
	if err := devCmd.Start(); err != nil {
		devLog.Close()
		return "", "", fmt.Errorf("start dev server: %w", err)
	}
	if err := devLog.Close(); err != nil {
		fmt.Fprintf(out, "warning: close dev log: %v\n", err)
	}
	fmt.Fprintf(out, "Dev server: http://localhost:%s (log: %s/dev.log)\n", portStr, dir)

	return fmt.Sprintf("%d", devCmd.Process.Pid), portStr, nil
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

// validateSDD checks that methodology-specific prerequisites are present in
// the repo before any worktree is created. Currently only "speckit" requires
// external skills (.claude/commands/speckit.*.md files).
func validateSDD(sddName, repoRoot string) error {
	if sddName != "speckit" {
		return nil
	}
	pattern := filepath.Join(repoRoot, ".claude", "commands", "speckit.*.md")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return fmt.Errorf(
			"SpecKit skills not found in this repo.\n\n" +
				"--sdd=speckit requires SpecKit slash commands installed as Claude Code command files:\n" +
				"  .claude/commands/speckit.specify.md\n" +
				"  .claude/commands/speckit.plan.md\n" +
				"  .claude/commands/speckit.tasks.md\n" +
				"  .claude/commands/speckit.implement.md\n\n" +
				"To install: copy the SpecKit skill files into .claude/commands/ in this repo.\n\n" +
				"Alternatively, use --sdd=plain which works without any additional setup:\n" +
				"  agentctl start <issue> --sdd=plain",
		)
	}
	return nil
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
func launchAgent(adapterName, wtPath, issue, port, sessionID, kickoff, sddName string, headless, quiet, sendNotify bool, out io.Writer) error {
	ad, err := adapters.Get(adapterName)
	if err != nil {
		return err
	}

	if err := ad.CheckBinary(); err != nil {
		return err
	}

	if headless && adapterName == "claude" {
		if err := writeClaudeSettings(wtPath); err != nil {
			return err
		}
	}

	agentCmd := ad.LaunchCmd(kickoff, sessionID, wtPath)
	agentCmd.Dir = wtPath
	agentEnv, err := agentEnv(wtPath)
	if err != nil {
		return fmt.Errorf("agentEnv: %w", err)
	}
	agentCmd.Env = agentEnv

	logPath := filepath.Join(wtPath, "agent.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create agent.log: %w", err)
	}

	// In interactive mode, capture output through a pipe so we can parse
	// stream-json events and write human-readable text to the log file
	// progressively. In headless mode, non-Claude adapters write directly to
	// the file; Claude headless uses a pipe fed to a detached __stream-log
	// subprocess so intermediate tool steps are captured progressively.
	var pr, pw *os.File
	if !headless {
		pr, pw, err = os.Pipe()
		if err != nil {
			logFile.Close()
			return fmt.Errorf("os.Pipe: %w", err)
		}
		// --output-format stream-json requires --verbose; both are Claude-specific.
		if adapterName == "claude" {
			agentCmd.Args = append(agentCmd.Args, "--output-format", "stream-json", "--verbose")
		}
		agentCmd.Stdout = pw
		agentCmd.Stderr = pw
		// Redirect stdin to /dev/null so the agent sees no TTY and does not
		// render an interactive UI that would overlap agentctl's log output.
		agentCmd.Stdin = nil
	} else {
		if adapterName == "claude" {
			// Use stream-json so intermediate tool steps are captured progressively.
			// Since the parent exits immediately in headless mode, a detached
			// __stream-log subprocess converts the pipe to human-readable text in
			// agent.log.
			agentCmd.Args = append(agentCmd.Args, "--output-format", "stream-json", "--verbose")
			pr, pw, err = os.Pipe()
			if err != nil {
				logFile.Close()
				return fmt.Errorf("os.Pipe: %w", err)
			}
			agentCmd.Stdout = pw
			agentCmd.Stderr = pw
		} else if adapterName == "openhands" {
			pr, pw, err = os.Pipe()
			if err != nil {
				logFile.Close()
				return fmt.Errorf("os.Pipe: %w", err)
			}
			agentCmd.Stdout = pw
			agentCmd.Stderr = pw
		} else {
			agentCmd.Stdout = logFile
			agentCmd.Stderr = logFile
		}
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
		if pw != nil {
			// Spawn a detached converter process. Its stdout is the already-open
			// logFile fd so it never opens files by path (avoids race with test
			// cleanup removing the temp dir).
			streamLogCmd := "__stream-log"
			if adapterName == "openhands" {
				streamLogCmd = "__stream-log-openhands"
			}
			convCmd := exec.Command(os.Args[0], streamLogCmd, wtPath)
			convCmd.Stdin = pr
			convCmd.Stdout = logFile
			convCmd.Stderr = logFile
			// Set Dir to wtPath so the converter's CWD is outside any temp dir
			// that the test may have set via chdirTemp, preventing any runtime
			// exit hooks from creating files in a directory under cleanup.
			convCmd.Dir = wtPath
			detachProcess(convCmd)
			if convErr := convCmd.Start(); convErr != nil {
				pw.Close()
				pr.Close()
				logFile.Close()
				return fmt.Errorf("start log converter: %w", convErr)
			}
			_ = convCmd.Process.Release()
			pw.Close()
			pr.Close()
		}
		logFile.Close()
	} else {
		// Close the write end in the parent; the child has its own copy.
		pw.Close()
		// Convert output events to readable text written to logFile.
		convWg.Add(1)
		go func() {
			defer convWg.Done()
			defer pr.Close()
			defer logFile.Close()

			if adapterName == "openhands" {
				convertOpenHandsStream(pr, logFile)
				return
			}
			if adapterName != "claude" {
				// Non-Claude adapters emit plain text; copy it directly to the log.
				if _, err := io.Copy(logFile, pr); err != nil && !errors.Is(err, io.ErrClosedPipe) {
					fmt.Fprintf(logFile, "converter read error: %v\n", err)
				}
				return
			}
			r := bufio.NewReader(pr)
			for {
				line, err := r.ReadString('\n')
				if line != "" {
					if text := extractStreamText(strings.TrimSuffix(line, "\n"), wtPath); text != "" {
						fmt.Fprintln(logFile, text)
					}
				}
				if err != nil {
					if !errors.Is(err, io.EOF) {
						fmt.Fprintf(logFile, "converter read error: %v\n", err)
					}
					break
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
	//
	// Memory model note: agentExitErr is written before close(exitCh) and
	// read after <-exitCh, so the channel close provides the necessary
	// happens-before guarantee; no additional synchronization is needed.
	var agentExitErr error
	exitCh := make(chan struct{})
	go func() {
		agentExitErr = agentCmd.Wait()
		if agentExitErr != nil {
			fmt.Fprintf(os.Stderr, "agent exited: %v\n", agentExitErr)
		}
		close(exitCh)
	}()

	// Record the agent PID in .agent (core fields were already written by runStart).
	if err := state.AppendKey(wtPath, "agent-pid", strconv.Itoa(pid)); err != nil {
		return err
	}

	if headless {
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-exitCh:
			if !timer.Stop() {
				<-timer.C
			}
			if agentExitErr != nil {
				exitCode := "unknown"
				var exitErr *exec.ExitError
				if errors.As(agentExitErr, &exitErr) {
					exitCode = strconv.Itoa(exitErr.ExitCode())
				}
				// Give the detached stream-log converter a moment to flush output.
				time.Sleep(200 * time.Millisecond)
				if content, readErr := os.ReadFile(logPath); readErr == nil && len(content) > 0 {
					fmt.Fprintf(out, "%s\n", content)
				}
				return fmt.Errorf("Agent exited immediately (code %s) — re-run without --headless to see live output.", exitCode)
			}
		case <-timer.C:
		}

		fmt.Fprintf(out, "Agent PID %d — log: %s\n", pid, logPath)
		fmt.Fprintf(out, "agentctl logs %s      # follow log\n", issue)
		fmt.Fprintf(out, "agentctl attach %s    # stream live and wait\n", issue)
		fmt.Fprintf(out, "agentctl discard %s   # abandon\n", issue)
		if sddName != "" {
			fmt.Fprintf(out, "agentctl resume %s [feedback]   # approve spec or send revisions\n", issue)
		}
		if sendNotify {
			maybeFireTestNotification(issue, out)
			go func() {
				<-exitCh
				sendCompletionNotification(issue, wtPath, sddName, agentExitErr)
			}()
		}
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
		followLog(logPath, out, logDone, quiet, true)
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
			if agentExitErr != nil {
				fmt.Fprintf(out, "agent exited with error — check the log: agentctl logs %s\n", issue)
				return nil
			}
			branch, branchErr := git.CurrentBranch(wtPath)
			if branchErr != nil {
				branch = ""
			}
			hasPR := reportPRStatus(out, wtPath, branch, issue)
			if sddName != "" {
				if specPath := findSpecPath(wtPath, issue); specPath != "" {
					fmt.Fprintf(out, "Spec: %s\n", specPath)
				}
				fmt.Fprintf(out, resumeHintFmt, issue)
			} else if hasPR {
				fmt.Fprintf(out, "agentctl cleanup %s   # after PR is merged\n", issue)
			}
			return nil
		case <-sigCh:
			signal.Stop(sigCh)
			close(logDone)
			wg.Wait()
			fmt.Fprintf(out, "agent still running in background\n")
			fmt.Fprintf(out, "  agentctl logs %s     # follow log\n", issue)
			fmt.Fprintf(out, "  agentctl attach %s   # stream live output\n", issue)
			fmt.Fprintf(out, "  agentctl discard %s  # permanently delete worktree and branches\n", issue)
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

// notifyTestSentinelPath returns the path of the one-time sentinel file that
// records whether the first-run notification test has already been done.
// Returns "" when the user config directory cannot be resolved.
func notifyTestSentinelPath() string {
	cfgDir := xdg.UserConfigDir()
	if cfgDir == "" {
		return ""
	}
	return filepath.Join(cfgDir, "agentctl", "notify-tested")
}

// maybeFireTestNotification sends a single test notification the very first
// time --notify is used, then creates a sentinel so it never fires again.
// A one-line hint is printed to out so the user knows to look for it.
func maybeFireTestNotification(issue string, out io.Writer) {
	sentinel := notifyTestSentinelPath()
	if sentinel == "" {
		return
	}

	if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err != nil {
		return
	}

	f, err := os.OpenFile(sentinel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return // already tested
		}
		return
	}
	_ = f.Close()

	notify.Send("agentctl", fmt.Sprintf("Notifications enabled — you'll be notified when issue #%s finishes.", issue))

	var hint string
	switch runtime.GOOS {
	case "darwin":
		hint = "Note: a test notification was sent — if you didn't see it, go to System Settings → Notifications → Terminal and set the style to \"Alerts\"."
	case "linux":
		hint = "Note: a test notification was sent — if you didn't see it, ensure notify-send (libnotify-bin) is installed."
	default:
		hint = "Note: a test notification was sent — if you didn't see it, check your system's notification settings."
	}
	fmt.Fprintln(out, hint)
}

// sendCompletionNotification fires a native desktop notification reporting
// that the agent for the given issue has finished. exitErr is the error
// returned by the agent process's Wait() call; nil means success.
// When sddName is non-empty and the agent succeeded, the message signals
// that the spec is ready for review rather than using the generic finish text.
func sendCompletionNotification(issue, wtPath, sddName string, exitErr error) {
	branch, _ := git.CurrentBranch(wtPath)
	branchPart := ""
	if branch != "" {
		branchPart = " (" + branch + ")"
	}
	var message string
	switch {
	case exitErr != nil:
		message = fmt.Sprintf("Agent failed — issue #%s%s: check agentctl logs %s", issue, branchPart, issue)
	case sddName != "" && findSpecPath(wtPath, issue) != "":
		specPath := findSpecPath(wtPath, issue)
		message = fmt.Sprintf("Spec ready for review — issue #%s%s: %s — agentctl resume %s", issue, branchPart, specPath, issue)
	default:
		message = fmt.Sprintf("Agent finished — issue #%s%s: succeeded", issue, branchPart)
	}
	notify.Send("agentctl", message)
}

// convertStreamToLog reads Claude --output-format stream-json lines from r,
// converts each event to human-readable text, and appends the result to
// logPath. Used by callers that need stream-json output persisted to a
// log file, including tests.
func convertStreamToLog(r io.Reader, logPath, wtDir string) error {
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	br := bufio.NewReader(r)
	for {
		line, readErr := br.ReadString('\n')
		if line != "" {
			if text := extractStreamText(strings.TrimSuffix(line, "\n"), wtDir); text != "" {
				fmt.Fprintln(f, text)
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				fmt.Fprintf(f, "stream-log read error: %v\n", readErr)
			}
			break
		}
	}
	return nil
}

// runStreamLog reads Claude --output-format stream-json lines from stdin,
// converts each event to human-readable text, and writes to stdout. The caller
// (agentctl headless launcher) sets the subprocess stdout to an already-open
// log file fd so no file-path lookup is needed.
func runStreamLog(wtDir string) error {
	r := bufio.NewReader(os.Stdin)
	for {
		line, readErr := r.ReadString('\n')
		if line != "" {
			if text := extractStreamText(strings.TrimSuffix(line, "\n"), wtDir); text != "" {
				fmt.Fprintln(os.Stdout, text)
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				fmt.Fprintf(os.Stdout, "stream-log read error: %v\n", readErr)
			}
			break
		}
	}
	return nil
}

// NewStreamLogCmd returns a hidden cobra command that agentctl spawns as a
// detached background process in headless mode. It reads Claude
// --output-format stream-json from stdin and writes human-readable text to
// stdout (which is the inherited agent.log file descriptor).
func NewStreamLogCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "__stream-log <wtDir>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runStreamLog(args[0])
		},
	}
}

// convertOpenHandsStream reads from r, which carries openhands --json output,
// and writes human-readable text to w. The format alternates between plain-text
// lines and multi-line JSON blocks delimited by "--JSON Event--" markers.
// Brace depth tracking detects the end of each JSON object so that plain-text
// lines between events (e.g. "Agent is working") are printed, not swallowed.
func convertOpenHandsStream(r io.Reader, w io.Writer) {
	const sep = "--JSON Event--"
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 512*1024), 512*1024)

	var buf strings.Builder
	inEvent := false
	depth := 0
	started := false // true once the opening '{' of the JSON object is seen

	flush := func() {
		if buf.Len() > 0 {
			if text := extractOpenHandsBlock(buf.String()); text != "" {
				fmt.Fprintln(w, text)
			}
			buf.Reset()
		}
		inEvent = false
		depth = 0
		started = false
	}

	for sc.Scan() {
		line := sc.Text()
		if line == sep {
			flush()
			inEvent = true
			continue
		}
		if inEvent {
			for _, ch := range line {
				switch ch {
				case '{':
					depth++
					started = true
				case '}':
					depth--
				}
			}
			buf.WriteString(line)
			buf.WriteByte('\n')
			// Once the top-level object closes, flush and revert to plain-text mode.
			if started && depth == 0 {
				flush()
			}
		} else if !isOpenHandsNoise(line) && line != "" {
			fmt.Fprintln(w, line)
		}
	}
	flush()
}

// isOpenHandsNoise returns true for lines that are Rich terminal UI artefacts
// and should be suppressed from the human-readable log.
func isOpenHandsNoise(line string) bool {
	return strings.Contains(line, "Rich detected a non-interactive") ||
		strings.Contains(line, "To override Rich's detection") ||
		strings.HasPrefix(line, "────") ||
		strings.HasPrefix(line, "╭") ||
		strings.HasPrefix(line, "│") ||
		strings.HasPrefix(line, "╰") ||
		strings.Contains(line, "CONVERSATION SUMMARY")
}

// extractOpenHandsBlock parses a complete openhands --json event JSON block
// and returns human-readable text, or "" to skip the event.
func extractOpenHandsBlock(jsonBlock string) string {
	var ev struct {
		Kind       string `json:"kind"`
		Source     string `json:"source"`
		LLMMessage *struct {
			Content []struct {
				Text string `json:"text"`
				Type string `json:"type"`
			} `json:"content"`
		} `json:"llm_message"`
		ToolName    string `json:"tool_name"`
		Summary     string `json:"summary"`
		Observation *struct {
			Content []struct {
				Text string `json:"text"`
				Type string `json:"type"`
			} `json:"content"`
			IsError bool `json:"is_error"`
		} `json:"observation"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(jsonBlock), &ev); err != nil {
		return ""
	}
	switch ev.Kind {
	case "MessageEvent":
		if ev.Source != "agent" || ev.LLMMessage == nil {
			return ""
		}
		var parts []string
		for _, c := range ev.LLMMessage.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				parts = append(parts, strings.TrimSpace(c.Text))
			}
		}
		return strings.Join(parts, "\n")
	case "ActionEvent":
		switch {
		case ev.ToolName != "" && ev.Summary != "":
			return fmt.Sprintf("[%s] %s", ev.ToolName, ev.Summary)
		case ev.ToolName != "":
			return fmt.Sprintf("[%s]", ev.ToolName)
		case ev.Summary != "":
			return ev.Summary
		}
		return ""
	case "ObservationEvent":
		if ev.Observation == nil || !ev.Observation.IsError {
			return ""
		}
		var parts []string
		for _, c := range ev.Observation.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				parts = append(parts, strings.TrimSpace(c.Text))
			}
		}
		return strings.Join(parts, "\n")
	case "AgentErrorEvent":
		if ev.Error != "" {
			return "error: " + ev.Error
		}
		return ""
	default:
		return ""
	}
}

// runStreamLogOpenHands reads openhands --json output from stdin and writes
// human-readable text to stdout. Used by the detached __stream-log-openhands
// subprocess in headless mode.
func runStreamLogOpenHands(_ string) error {
	convertOpenHandsStream(os.Stdin, os.Stdout)
	return nil
}

// NewStreamLogOpenHandsCmd returns a hidden cobra command that agentctl spawns
// as a detached background process in headless mode to convert openhands --json
// output to human-readable text in agent.log.
func NewStreamLogOpenHandsCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "__stream-log-openhands <wtDir>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runStreamLogOpenHands(args[0])
		},
	}
}

// extractStreamText converts a single claude --output-format stream-json line
// into human-readable text. wtDir is the worktree directory; file paths inside
// it are shown as relative paths to reduce noise in terminal output. It
// extracts assistant text and tool-use blocks. "result" events are
// intentionally ignored because their text duplicates the final "assistant"
// content block, which would otherwise cause the PR link and closing summary
// to appear twice in terminal output. Non-JSON lines are dropped.
func extractStreamText(line, wtDir string) string {
	var ev struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return "" // not JSON — drop; in stream-json mode all meaningful output is JSON
	}
	switch ev.Type {
	case "assistant":
		var sb strings.Builder
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "text":
				if t := strings.TrimSpace(c.Text); t != "" {
					sb.WriteString(highlightPRURLs(t))
					sb.WriteByte('\n')
				}
			case "tool_use":
				fmt.Fprintf(&sb, "[%s]\n", toolLabel(c.Name, c.Input, wtDir))
			}
		}
		return strings.TrimSuffix(sb.String(), "\n")
	}
	return ""
}

var githubPRURL = regexp.MustCompile(`https://github\.com/[^/]+/[^/]+/pull/\d+`)

func highlightPRURLs(text string) string {
	return githubPRURL.ReplaceAllStringFunc(text, func(url string) string {
		return "\n>>> " + url + "\n"
	})
}

// toolDetailMaxLen is the maximum number of runes shown for a tool input detail
// before it is truncated with an ellipsis.
const toolDetailMaxLen = 120

// sanitizeDetail normalises a raw tool-input string for terminal display:
// leading/trailing whitespace is stripped, internal whitespace runs (including
// newlines) are collapsed to a single space, and the result is truncated to
// toolDetailMaxLen runes.
func sanitizeDetail(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > toolDetailMaxLen {
		return string(r[:toolDetailMaxLen]) + "..."
	}
	return s
}

// toolLabel returns a display string for a tool_use block, including the most
// useful input field for the given tool so the terminal output is actionable.
// File paths are shown relative to wtDir when they fall inside it.
// Set AGENTCTL_NO_TOOL_DETAIL=1 to suppress input details (e.g. to avoid
// echoing sensitive data to the terminal).
func toolLabel(name string, input json.RawMessage, wtDir string) string {
	if os.Getenv("AGENTCTL_NO_TOOL_DETAIL") != "" {
		return name
	}
	var detail string
	switch strings.ToLower(name) {
	case "bash":
		var v struct {
			Command     string `json:"command"`
			Description string `json:"description"`
		}
		if json.Unmarshal(input, &v) == nil {
			if v.Description != "" {
				detail = v.Description
			} else if v.Command != "" {
				detail = v.Command
			}
		}
	case "read", "write", "edit":
		var v struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal(input, &v) == nil && v.FilePath != "" {
			detail = relativePath(v.FilePath, wtDir)
		}
	case "websearch":
		var v struct {
			Query string `json:"query"`
		}
		if json.Unmarshal(input, &v) == nil && v.Query != "" {
			detail = v.Query
		}
	case "webfetch":
		var v struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(input, &v) == nil && v.URL != "" {
			detail = v.URL
		}
	}
	if detail == "" {
		return name
	}
	return name + ": " + sanitizeDetail(detail)
}

// relativePath returns path relative to base when path is inside base,
// otherwise returns path unchanged.
func relativePath(path, base string) string {
	if base == "" {
		return path
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return filepath.ToSlash(rel)
}

// stackFrameRe matches JavaScript/Node.js stack-frame lines, e.g.
//
//	at Gaxios._request (file:///path/bundle.js:8578:19)
var stackFrameRe = regexp.MustCompile(`^\s+at\s+\S`)

// isStderrNoise reports whether line is verbose agent error noise that should
// be suppressed from the terminal stream. It matches JavaScript/Node.js stack
// frames and standalone JSON blobs. The raw line is still written to agent.log.
func isStderrNoise(line string) bool {
	if stackFrameRe.MatchString(line) {
		return true
	}
	trimmed := strings.TrimSpace(line)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid([]byte(trimmed)) {
		return true
	}
	return false
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
func followLog(logPath string, out io.Writer, done <-chan struct{}, quiet bool, filterNoise bool) {
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
				if !filterNoise || !isStderrNoise(strings.TrimRight(line, "\r\n")) {
					clearSpinner()
					// On a TTY, partial lines (no trailing \n) leave the cursor
					// mid-line. The next spinner tick then uses \r to reposition
					// to column 0 and overwrites the log text. Force a newline so
					// the cursor always lands at a clean line start.
					if isTTY && !strings.HasSuffix(line, "\n") {
						fmt.Fprintln(out, line)
					} else {
						fmt.Fprint(out, line)
					}
				}
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

// agentEnv builds the environment for a spawned agent process. It overrides
// HOME to a private directory inside the worktree so the agent gets a fresh
// ~/.claude (no existing sessions to attach to) while every other tool keeps
// working. Git and SSH stay functional via symlinks into the real home.
// Returns an error if the private HOME directory cannot be created.
func agentEnv(wtPath string) ([]string, error) {
	agentHome := filepath.Join(wtPath, ".agent-home")

	// Guard against a pre-existing symlink at .agent-home: a malicious repo
	// could plant one to redirect HOME outside the worktree and bypass isolation.
	if fi, err := os.Lstat(agentHome); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("agentEnv: .agent-home is a symlink; refusing to use as HOME")
	}

	if err := os.MkdirAll(agentHome, 0o755); err != nil {
		return nil, fmt.Errorf("agentEnv: create agent home dir: %w", err)
	}

	realHome, err := os.UserHomeDir()
	if err == nil {
		// Link config files/dirs so git, SSH, Claude, OpenHands, and Codex work with real credentials/settings.
		for _, name := range []string{".gitconfig", ".ssh", ".claude", ".claude.json", ".openhands", ".codex"} {
			src := filepath.Join(realHome, name)
			dst := filepath.Join(agentHome, name)

			// Keep existing symlinks, but plan to replace stale regular files for
			// entries that must always point at live host credentials.
			var dstNeedsReplacement bool
			if dstInfo, statErr := os.Lstat(dst); statErr == nil {
				if dstInfo.Mode()&os.ModeSymlink != 0 {
					continue
				}
				if name != ".claude.json" {
					continue
				}
				// dst is a regular .claude.json; replace it with a live symlink.
				dstNeedsReplacement = true
			} else if !os.IsNotExist(statErr) {
				fmt.Fprintf(os.Stderr, "agentctl: warning: stat %s: %v\n", dst, statErr)
				continue
			}

			// Only proceed when src actually exists; warn on unexpected src errors.
			// Removal of a stale dst is deferred until after src existence is confirmed.
			if _, srcErr := os.Lstat(src); srcErr != nil {
				if !os.IsNotExist(srcErr) {
					fmt.Fprintf(os.Stderr, "agentctl: warning: stat %s: %v\n", src, srcErr)
				}
				continue
			}

			// src exists; now it is safe to remove the stale dst.
			if dstNeedsReplacement {
				if removeErr := os.Remove(dst); removeErr != nil {
					fmt.Fprintf(os.Stderr, "agentctl: warning: remove %s: %v\n", dst, removeErr)
					continue
				}
			}

			if symlinkErr := os.Symlink(src, dst); symlinkErr != nil {
				// On Windows, symlinks require Developer Mode. Fall back to
				// copying regular files; for directories emit a warning so
				// failures are diagnosable.
				// Use os.Stat to follow symlinks when determining directory-ness.
				if srcStat, statErr := os.Stat(src); statErr != nil || !srcStat.IsDir() {
					if copyErr := copyFile(src, dst); copyErr != nil {
						fmt.Fprintf(os.Stderr, "agentctl: warning: copy %s: %v\n", name, copyErr)
					}
				} else {
					fmt.Fprintf(os.Stderr, "agentctl: warning: symlink %s: %v (agent config may not work)\n", name, symlinkErr)
				}
			}
		}

		// Expose only ~/.config/gh (the gh CLI credential store) rather than
		// the entire ~/.config tree, to limit the host config surface accessible
		// from the agent's isolated HOME.
		ghConfigSrc := filepath.Join(realHome, ".config", "gh")
		if _, srcErr := os.Lstat(ghConfigSrc); srcErr == nil {
			agentConfigDir := filepath.Join(agentHome, ".config")
			if mkdirErr := os.MkdirAll(agentConfigDir, 0o755); mkdirErr != nil {
				fmt.Fprintf(os.Stderr, "agentctl: warning: mkdir %s: %v\n", agentConfigDir, mkdirErr)
			} else {
				ghConfigDst := filepath.Join(agentConfigDir, "gh")
				if _, statErr := os.Lstat(ghConfigDst); os.IsNotExist(statErr) {
					if symlinkErr := os.Symlink(ghConfigSrc, ghConfigDst); symlinkErr != nil {
						fmt.Fprintf(os.Stderr, "agentctl: warning: symlink .config/gh: %v (gh credentials may not work)\n", symlinkErr)
					}
				}
			}
		} else if !os.IsNotExist(srcErr) {
			fmt.Fprintf(os.Stderr, "agentctl: warning: stat %s: %v\n", ghConfigSrc, srcErr)
		}
	}

	env := os.Environ()

	// If GITHUB_TOKEN is absent or empty, pull it from `gh auth token` so
	// the agent can push to GitHub without needing keychain access (the agent
	// process runs detached and cannot unlock the macOS keychain).
	hasGHToken := false
	ghTokenIdx := -1
	for i, kv := range env {
		if strings.HasPrefix(kv, "GITHUB_TOKEN=") {
			ghTokenIdx = i
			if strings.TrimPrefix(kv, "GITHUB_TOKEN=") != "" {
				hasGHToken = true
			}
			break
		}
	}
	if !hasGHToken {
		if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
			if token := strings.TrimSpace(string(out)); token != "" {
				if ghTokenIdx >= 0 {
					env[ghTokenIdx] = "GITHUB_TOKEN=" + token
				} else {
					env = append(env, "GITHUB_TOKEN="+token)
				}
			}
		}
	}

	for i, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			env[i] = "HOME=" + agentHome
			return env, nil
		}
	}
	return append(env, "HOME="+agentHome), nil
}

// copyFile copies the file at src to dst using regular file I/O.
// It is used as a Windows fallback when os.Symlink is unavailable.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() // read-only; Close error is safe to ignore

	fi, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode())
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// writeClaudeSettings creates .claude/settings.json in wtPath with a
// wildcard allow-list so that sub-agents spawned by the top-level Claude
// process inherit the same bypass-permissions mode. If the file already
// exists (e.g. committed in the repo) it is left untouched.
func writeClaudeSettings(wtPath string) error {
	dir := filepath.Join(wtPath, ".claude")
	// Reject symlinks to prevent writing outside the worktree.
	if fi, err := os.Lstat(dir); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf(".claude is a symlink; refusing to write settings to an unverified location")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create .claude dir: %w", err)
	}
	dst := filepath.Join(dir, "settings.json")
	if _, err := os.Stat(dst); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat claude settings: %w", err)
	}
	data, err := json.Marshal(map[string]any{
		"permissions": map[string]any{
			"allow": []string{"*"},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal claude settings: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("write .claude/settings.json: %w", err)
	}
	return nil
}

// agentResume starts the coding agent in resume mode using the named adapter.
// When headless is false (default) it streams agent.log to the terminal and
// blocks until the agent exits. When headless is true it detaches immediately
// and writes output to agent.log.
func agentResume(adapterName, wtPath, issue, sessionID, prompt string, headless, quiet, sendNotify bool) error {
	ad, err := adapters.Get(adapterName)
	if err != nil {
		return err
	}

	if err := ad.CheckBinary(); err != nil {
		return err
	}

	if headless && adapterName == "claude" {
		if err := writeClaudeSettings(wtPath); err != nil {
			return err
		}
	}

	resumeCmd := ad.ResumeCmd(prompt, sessionID, wtPath)
	resumeCmd.Dir = wtPath
	resumeEnv, err := agentEnv(wtPath)
	if err != nil {
		return fmt.Errorf("agentEnv: %w", err)
	}
	resumeCmd.Env = resumeEnv

	logPath := filepath.Join(wtPath, "agent.log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open agent.log for append: %w", err)
	}

	var pr, pw *os.File
	if !headless {
		pr, pw, err = os.Pipe()
		if err != nil {
			logFile.Close()
			return fmt.Errorf("os.Pipe: %w", err)
		}
		if adapterName == "claude" {
			resumeCmd.Args = append(resumeCmd.Args, "--output-format", "stream-json", "--verbose")
		}
		resumeCmd.Stdout = pw
		resumeCmd.Stderr = pw
		resumeCmd.Stdin = nil
	} else {
		if adapterName == "claude" {
			// Use stream-json so intermediate tool steps are captured progressively
			// (same fix as launchAgent headless path).
			resumeCmd.Args = append(resumeCmd.Args, "--output-format", "stream-json", "--verbose")
			pr, pw, err = os.Pipe()
			if err != nil {
				logFile.Close()
				return fmt.Errorf("os.Pipe: %w", err)
			}
			resumeCmd.Stdout = pw
			resumeCmd.Stderr = pw
		} else if adapterName == "openhands" {
			pr, pw, err = os.Pipe()
			if err != nil {
				logFile.Close()
				return fmt.Errorf("os.Pipe: %w", err)
			}
			resumeCmd.Stdout = pw
			resumeCmd.Stderr = pw
		} else {
			resumeCmd.Stdout = logFile
			resumeCmd.Stderr = logFile
		}
	}

	detachProcess(resumeCmd)

	if err := resumeCmd.Start(); err != nil {
		if pw != nil {
			pw.Close()
			pr.Close()
		}
		logFile.Close()
		return fmt.Errorf("agent resume failed to start: %w", err)
	}

	var convWg sync.WaitGroup
	if headless {
		if pw != nil {
			streamLogCmd := "__stream-log"
			if adapterName == "openhands" {
				streamLogCmd = "__stream-log-openhands"
			}
			convCmd := exec.Command(os.Args[0], streamLogCmd, wtPath)
			convCmd.Stdin = pr
			convCmd.Stdout = logFile
			convCmd.Stderr = logFile
			convCmd.Dir = wtPath
			detachProcess(convCmd)
			if convErr := convCmd.Start(); convErr != nil {
				pw.Close()
				pr.Close()
				logFile.Close()
				return fmt.Errorf("start log converter: %w", convErr)
			}
			_ = convCmd.Process.Release()
			pw.Close()
			pr.Close()
		}
		logFile.Close()
	} else {
		pw.Close()
		convWg.Add(1)
		go func() {
			defer convWg.Done()
			defer pr.Close()
			defer logFile.Close()

			if adapterName == "openhands" {
				convertOpenHandsStream(pr, logFile)
				return
			}
			if adapterName != "claude" {
				// Non-Claude adapters emit plain text; copy it directly to the log.
				if _, err := io.Copy(logFile, pr); err != nil && !errors.Is(err, io.ErrClosedPipe) {
					fmt.Fprintf(logFile, "converter read error: %v\n", err)
				}
				return
			}
			r := bufio.NewReader(pr)
			for {
				line, err := r.ReadString('\n')
				if line != "" {
					if text := extractStreamText(strings.TrimSuffix(line, "\n"), wtPath); text != "" {
						fmt.Fprintln(logFile, text)
					}
				}
				if err != nil {
					if !errors.Is(err, io.EOF) {
						fmt.Fprintf(logFile, "converter read error: %v\n", err)
					}
					break
				}
			}
		}()
	}

	pid := resumeCmd.Process.Pid

	// Memory model note: resumeExitErr is written before close(exitCh) and
	// read after <-exitCh, so the channel close provides the necessary
	// happens-before guarantee; no additional synchronization is needed.
	var resumeExitErr error
	exitCh := make(chan struct{})
	go func() {
		resumeExitErr = resumeCmd.Wait()
		if resumeExitErr != nil {
			fmt.Fprintf(os.Stderr, "agent exited: %v\n", resumeExitErr)
		}
		close(exitCh)
	}()

	if err := state.AppendKey(wtPath, "agent-pid", strconv.Itoa(pid)); err != nil {
		return err
	}

	if headless {
		fmt.Printf("Released pause for issue %s; Stage 2 running in background.\n", issue)
		fmt.Printf("agentctl logs %s      # follow log\n", issue)
		fmt.Printf("agentctl attach %s    # stream live and wait\n", issue)
		fmt.Printf("agentctl discard %s   # abandon\n", issue)
		if sendNotify {
			maybeFireTestNotification(issue, os.Stdout)
			go func() {
				<-exitCh
				sendCompletionNotification(issue, wtPath, "", resumeExitErr)
			}()
		}
		return nil
	}

	if err := waitForFile(logPath, 10*time.Second); err != nil {
		return err
	}

	mirrorResumeLogFromOffset := func(srcPath string, dst *os.File, offset int64, done <-chan struct{}) {
		src, err := os.Open(srcPath)
		if err != nil {
			return
		}
		defer src.Close()

		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			default:
			}

			info, err := src.Stat()
			if err != nil {
				return
			}

			size := info.Size()
			if size < offset {
				offset = size
			}

			if size > offset {
				if _, err := src.Seek(offset, io.SeekStart); err != nil {
					return
				}
				written, err := io.Copy(dst, io.LimitReader(src, size-offset))
				offset += written
				if err != nil {
					return
				}
				if err := dst.Sync(); err != nil {
					return
				}
			}

			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}

	followResumeLogFromEOF := func(srcPath string, out io.Writer, done <-chan struct{}, quiet bool, color bool) {
		info, err := os.Stat(srcPath)
		if err != nil {
			followLog(srcPath, out, done, quiet, color)
			return
		}

		tmp, err := os.CreateTemp("", "agentctl-resume-log-*")
		if err != nil {
			followLog(srcPath, out, done, quiet, color)
			return
		}
		tmpPath := tmp.Name()
		defer func() {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}()

		mirrorDone := make(chan struct{})
		go func() {
			defer close(mirrorDone)
			mirrorResumeLogFromOffset(srcPath, tmp, info.Size(), done)
		}()

		followLog(tmpPath, out, done, quiet, color)
		<-mirrorDone
	}

	logDone := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		followResumeLogFromEOF(logPath, os.Stdout, logDone, quiet, true)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	for {
		select {
		case <-exitCh:
			signal.Stop(sigCh)
			convWg.Wait()
			close(logDone)
			wg.Wait()
			branch, branchErr := git.CurrentBranch(wtPath)
			if branchErr != nil {
				branch = ""
			}
			hasPR := reportPRStatus(os.Stdout, wtPath, branch, issue)
			if hasPR {
				fmt.Fprintf(os.Stdout, "agentctl cleanup %s   # after PR is merged\n", issue)
			}
			return nil
		case <-sigCh:
			signal.Stop(sigCh)
			close(logDone)
			wg.Wait()
			fmt.Fprintf(os.Stdout, "agent still running in background\n")
			fmt.Fprintf(os.Stdout, "  agentctl logs %s     # follow log\n", issue)
			fmt.Fprintf(os.Stdout, "  agentctl attach %s   # stream live output\n", issue)
			fmt.Fprintf(os.Stdout, "  agentctl discard %s  # permanently delete worktree and branches\n", issue)
			return nil
		}
	}
}
