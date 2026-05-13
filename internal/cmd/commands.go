// Package cmd implements the cobra subcommands for agentctl.
package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	"github.com/arun-gupta/agentctl/internal/diagnostics"
	"github.com/arun-gupta/agentctl/internal/git"
	"github.com/arun-gupta/agentctl/internal/notify"
	"github.com/arun-gupta/agentctl/internal/process"
	"github.com/arun-gupta/agentctl/internal/sdd"
	"github.com/arun-gupta/agentctl/internal/state"
	"github.com/arun-gupta/agentctl/internal/vcs"
	"github.com/arun-gupta/agentctl/internal/xdg"
)

// ─── start ────────────────────────────────────────────────────────────────────

// NewStartCmd creates the `start` subcommand.
func NewStartCmd() *cobra.Command {
	var (
		agentName  string
		branch     string
		headless   bool
		quiet      bool
		sddName    string
		sendNotify bool
		task       string
	)
	c := &cobra.Command{
		Use:   "start [<issue-number-or-url>[,<issue>...] [slug]]",
		Short: "Start work in an isolated worktree",
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
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if task != "" && len(args) > 0 {
				return fmt.Errorf("--task and a positional issue argument are mutually exclusive")
			}
			if branch != "" && task == "" {
				return fmt.Errorf("--branch requires --task")
			}
			// Apply per-project default_agent when --agent was not explicitly set.
			if !cmd.Flags().Changed("agent") {
				root, err := startConfigRepoRoot(args, os.Stdout)
				if err != nil {
					return fmt.Errorf("failed to determine repository for configuration: %w", err)
				}
				if root != "" {
					cfg, cfgErr := config.ReadMerged(root)
					if cfgErr != nil {
						return fmt.Errorf("read merged configuration (%s, %s): %w", filepath.Join(root, config.Filename), config.GlobalPath(), cfgErr)
					}
					if cfg.DefaultAgent != "" {
						agentName = cfg.DefaultAgent
					}
				}
			}

			if task != "" {
				return startTask(task, branch, agentName, sddName, headless, quiet, sendNotify, os.Stdout)
			}
			if len(args) == 0 {
				return fmt.Errorf("accepts between 1 and 2 arg(s), received 0")
			}

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

			// Multi-agent mode: --agent claude,codex with a single issue.
			agents := strings.Split(agentName, ",")
			for i, ag := range agents {
				agents[i] = strings.TrimSpace(ag)
			}
			// Drop empty entries (e.g. from trailing commas like "claude,").
			{
				filtered := agents[:0]
				for _, ag := range agents {
					if ag != "" {
						filtered = append(filtered, ag)
					}
				}
				agents = filtered
			}
			if len(agents) == 0 {
				return fmt.Errorf("--agent flag requires at least one agent name")
			}
			if len(agents) > 1 {
				// Reject duplicate agent names to avoid racing to create the same worktree.
				seen := make(map[string]struct{}, len(agents))
				for _, ag := range agents {
					if _, dup := seen[ag]; dup {
						return fmt.Errorf("duplicate agent %q in --agent flag", ag)
					}
					seen[ag] = struct{}{}
				}
				if len(issues) > 1 {
					return fmt.Errorf("cannot combine multiple issues with multiple agents; specify a single issue")
				}
				if slug != "" {
					return fmt.Errorf("[slug] argument is not supported when starting multiple agents")
				}
				return runMultiAgent(issues[0], agents, sddName, quiet, sendNotify, os.Stdout, os.Stderr)
			}

			if len(issues) > 1 {
				if slug != "" {
					return fmt.Errorf("[slug] argument is not supported when starting multiple issues")
				}
				return runBatch(issues, agentName, sddName, quiet, sendNotify, startOne, os.Stdout, os.Stderr)
			}

			return startOne(issues[0], slug, agentName, sddName, "", headless, quiet, sendNotify, os.Stdout)
		},
	}
	c.Flags().StringVar(&agentName, "agent", "claude", "Coding agent adapter to use")
	c.Flags().StringVar(&branch, "branch", "", "Explicit branch name (only valid with --task)")
	c.Flags().BoolVar(&headless, "headless", false, "Run agent in background (log -> agent.log)")
	c.Flags().BoolVar(&quiet, "quiet", false, "Suppress agent log output; show spinner/heartbeat only")
	c.Flags().StringVar(&sddName, "sdd", "", "SDD methodology to use (e.g. plain, speckit, or custom); omit to skip SDD")
	c.Flags().StringVar(&task, "task", "", "Free-form task description (alternative to a GitHub issue)")
	c.Flags().BoolVar(&sendNotify, "notify", false, "Send a desktop notification when the headless agent finishes")
	return c
}

// startConfigRepoRoot returns the repo root used for loading start-time config.
// For single issue URLs, this resolves/locates the target repository so
// default_agent is honoured even when invoked outside that repo.
func startConfigRepoRoot(args []string, out io.Writer) (string, error) {
	if len(args) == 0 {
		root, err := git.RepoRoot()
		if err != nil {
			return "", nil
		}
		return root, nil
	}
	rawIssues := strings.Split(args[0], ",")
	issues := make([]string, 0, len(rawIssues))
	for _, iss := range rawIssues {
		if s := strings.TrimSpace(iss); s != "" {
			issues = append(issues, s)
		}
	}
	if len(issues) == 1 {
		if isSupportedIssueURL(issues[0]) {
			root, _, _, _, err := repoRootForIssue(issues[0], out)
			if err != nil {
				return "", err
			}
			return root, nil
		}
	}
	root, err := git.RepoRoot()
	if err != nil {
		return "", nil
	}
	return root, nil
}

func isSupportedIssueURL(arg string) bool {
	_, _, _, ok := parseIssueURL(arg)
	return ok
}

// resumeHintFmt is printed after a foreground agent exits so users know how
// to send follow-up feedback. %s is replaced with the issue number.
const resumeHintFmt = "agentctl resume %s [feedback]   # no feedback approves; add feedback to request changes\n"

// kickoffTemplate is the default prompt sent to the agent when no --sdd
// methodology is specified. {platform}, {issue}, {prTerm}, and {port} are
// substituted at call time.
const kickoffTemplate = `Work on {platform} issue #{issue}. Read AGENTS.md or README.md for project conventions if present.
Make the changes directly, push the branch, and open a {prTerm}. Do not merge.
You are the coding agent — implement changes using your own file-editing and bash tools.
Do not run agentctl, claude, codex, or any other agent-launcher CLI.
When showing agentctl commands to the user, use {issue} as the identifier — not a full URL.
Before opening the PR, run the project's test suite (use test_cmd from
.agentctl.yml if present, otherwise infer from AGENTS.md or README.md).
In the PR description include a ## Test plan section with two subsections:
- Automated: the command run and a pass/fail summary.
- Manual: a bulleted list of scenarios that cannot be automated, each with
  a one-line explanation of why manual verification is needed.
If tests fail, fix the failures before opening the PR. If you cannot fix
them, open the PR anyway but clearly mark the failing tests in the plan.
Dev server is running on port {port}.`

// buildKickoff returns the default agent kickoff prompt for a plain
// (non-SDD) run with {issue}, {platform}, {prTerm}, and {port} substituted.
// When port is empty, the dev-server line is omitted entirely.
func buildKickoff(issue, port, platform, prTerm string) string {
	s := strings.ReplaceAll(kickoffTemplate, "{issue}", issue)
	s = strings.ReplaceAll(s, "{platform}", platform)
	s = strings.ReplaceAll(s, "{prTerm}", prTerm)
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

// buildKickoffFromTask returns the default agent kickoff prompt for a
// free-form task run. When port is empty, the dev-server line is omitted.
// prTerm is "PR" or "MR" depending on the VCS provider.
func buildKickoffFromTask(task, port, prTerm string) string {
	s := "Work on the following task: " + task + "\n" +
		"Make the changes directly, push the branch, and open a " + prTerm + ". Do not merge.\n" +
		"You are the coding agent — implement changes using your own file-editing and bash tools.\n" +
		"Do not run agentctl, claude, codex, or any other agent-launcher CLI.\n" +
		"When showing agentctl commands to the user, use the branch name as the identifier — not a full URL.\n" +
		"Before opening the PR, run the project's test suite (use test_cmd from\n" +
		".agentctl.yml if present, otherwise infer from AGENTS.md or README.md).\n" +
		"In the PR description include a '## Test plan' section with two subsections:\n" +
		"- Automated: the command run and a pass/fail summary.\n" +
		"- Manual: a bulleted list of scenarios that cannot be automated, each with\n" +
		"  a one-line explanation of why manual verification is needed.\n" +
		"If tests fail, fix the failures before opening the PR. If you cannot fix\n" +
		"them, open the PR anyway but clearly mark the failing tests in the plan."
	if port != "" {
		s += "\nDev server is running on port " + port + "."
	}
	return s
}

// resolveProvider returns the VCS provider for the repository at repoRoot.
func resolveProvider(repoRoot string) (vcs.Provider, error) {
	return vcs.Detect(repoRoot)
}

// startOne provisions a worktree for a single issue and launches the agent.
// It is the per-issue unit used by both single-issue and batch invocations.
// agentSuffix is non-empty only in multi-agent mode (--agent claude,codex);
// it is appended to the branch name and worktree path to avoid collisions.
func startOne(issue, slug, agentName, sddName, agentSuffix string, headless, quiet, sendNotify bool, out io.Writer) error {
	startedAt := time.Now()

	// Validate the adapter exists before doing any setup work.
	if err := validateAdapter(agentName); err != nil {
		return err
	}

	// Resolve the repo root, issue number, and VCS provider.
	// issue may be a bare number ("42") or a full issue URL.
	repoRoot, issueNum, issueArg, p, err := repoRootForIssue(issue, out)
	if err != nil {
		return err
	}

	// Verify VCS credentials are available before any provider calls.
	if err := p.AuthCheck(); err != nil {
		return err
	}

	parentDir := filepath.Dir(repoRoot)
	repoName := filepath.Base(repoRoot)

	// Combine --notify flag with the per-repo config setting (either enables it).
	// notify: true in .agentctl.yml is only meaningful in headless mode.
	if !sendNotify {
		if cfg, cfgErr := config.ReadMerged(repoRoot); cfgErr == nil && cfg.Notify != nil && *cfg.Notify {
			sendNotify = true
		}
	}

	// Resolve default SDD from config when --sdd was not passed.
	// config.SDDNone ("none") means explicitly no SDD, so we leave sddName "".
	if sddName == "" {
		if cfg, cfgErr := config.ReadMerged(repoRoot); cfgErr == nil &&
			cfg.DefaultSDD != "" && cfg.DefaultSDD != config.SDDNone {
			sddName = cfg.DefaultSDD
		}
	}

	// Validate methodology-specific prerequisites before any side effects.
	if err := validateSDD(sddName, repoRoot); err != nil {
		return err
	}

	// Derive slug from issue title if not supplied.
	if slug == "" {
		title, titleErr := p.FetchIssueTitle(issueArg)
		if titleErr != nil {
			return titleErr
		}
		slug = titleToSlug(title)
		if slug == "" {
			slug = "work"
		}
		fmt.Fprintf(out, "Derived slug from issue title: %s\n", slug)
	}

	// Validate agentSuffix before using it in branch/path construction.
	if err := validateAgentSuffix(agentSuffix); err != nil {
		return err
	}

	branch, wtPath := worktreeNames(repoName, issueNum, slug, agentSuffix, parentDir)

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
		IssueArg:  issueArg,
	}
	if err := state.Write(wtPath, af); err != nil {
		return err
	}

	// Write initial diagnostics record and store the filename in .agent so later
	// commands can locate and update it.
	if isDiagnosticsEnabled(repoRoot) {
		dr := &diagnostics.RunRecord{
			Issue:      issueNum,
			Branch:     branch,
			Agent:      agentName,
			StartedAt:  startedAt,
			ExitReason: "in_progress",
		}
		if runFile, diagErr := diagnostics.Write(repoRoot, dr); diagErr == nil {
			_ = state.AppendKey(wtPath, "run-file", runFile)
		}
	}

	var kickoff string
	if sddName == "" {
		kickoff = buildKickoff(issueNum, portStr, p.Platform(), p.PRTerm())
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

// startTask provisions a worktree for a free-form task and launches the agent.
func startTask(task, branch, agentName, sddName string, headless, quiet, sendNotify bool, out io.Writer) error {
	startedAt := time.Now()

	if strings.TrimSpace(task) == "" {
		return fmt.Errorf("--task requires a non-empty task description")
	}
	if err := validateAdapter(agentName); err != nil {
		return err
	}

	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}

	p, err := resolveProvider(repoRoot)
	if err != nil {
		return err
	}

	parentDir := filepath.Dir(repoRoot)
	repoName := filepath.Base(repoRoot)

	if !sendNotify {
		if cfg, cfgErr := config.ReadMerged(repoRoot); cfgErr == nil && cfg.Notify != nil && *cfg.Notify {
			sendNotify = true
		}
	}

	// Resolve default SDD from config when --sdd was not passed.
	// config.SDDNone ("none") means explicitly no SDD, so we leave sddName "".
	if sddName == "" {
		if cfg, cfgErr := config.ReadMerged(repoRoot); cfgErr == nil &&
			cfg.DefaultSDD != "" && cfg.DefaultSDD != config.SDDNone {
			sddName = cfg.DefaultSDD
		}
	}

	if err := validateSDD(sddName, repoRoot); err != nil {
		return err
	}

	if branch == "" {
		branch = slugFromTask(task)
		fmt.Fprintf(out, "Derived branch from task: %s\n", branch)
	}

	worktreeSlug := strings.ReplaceAll(branch, "/", "-")
	wtPath := filepath.Join(parentDir, repoName+"-"+worktreeSlug)

	if _, statErr := os.Stat(wtPath); statErr == nil {
		return taskWorktreeExistsError(wtPath, branch)
	}
	if err := git.AddWorktree(repoRoot, wtPath, branch); err != nil {
		return fmt.Errorf("git worktree add: %w", err)
	}
	var devPID, portStr string
	cleanupOnError := true
	defer func() {
		if !cleanupOnError {
			return
		}
		if cleanupErr := cleanupFailedStart(repoRoot, wtPath, branch, devPID); cleanupErr != nil {
			fmt.Fprintf(out, "Warning: failed to clean up worktree after start failure (path: %s, branch: %s): %v\n", wtPath, branch, cleanupErr)
		}
	}()

	if err := seedEnvLocal(filepath.Join(repoRoot, ".env.local"), filepath.Join(wtPath, ".env.local")); err != nil {
		return err
	}

	devPID, portStr, err = startDevServer(wtPath, out)
	if err != nil {
		return err
	}

	sessionID, err := generateUUID()
	if err != nil {
		return fmt.Errorf("generate session ID: %w", err)
	}

	af := state.AgentFile{
		Agent:     agentName,
		SessionID: sessionID,
		DevPID:    devPID,
		DevPort:   portStr,
		SDD:       sddName,
		TaskMode:  true,
	}
	if err := state.Write(wtPath, af); err != nil {
		return err
	}

	// Write initial diagnostics record for task-mode runs.
	if isDiagnosticsEnabled(repoRoot) {
		dr := &diagnostics.RunRecord{
			Issue:      branch, // task mode: use branch name as the identifier
			Branch:     branch,
			Agent:      agentName,
			StartedAt:  startedAt,
			ExitReason: "in_progress",
		}
		if runFile, diagErr := diagnostics.Write(repoRoot, dr); diagErr == nil {
			_ = state.AppendKey(wtPath, "run-file", runFile)
		}
	}

	var kickoff string
	if sddName == "" {
		kickoff = buildKickoffFromTask(task, portStr, p.PRTerm())
	} else {
		m, sddErr := sdd.Get(sddName)
		if sddErr != nil {
			return sddErr
		}
		kickoff = m.KickoffPrompt(branch, portStr) + "\n\nTask description: " + task
	}

	if err := launchAgent(agentName, wtPath, branch, portStr, sessionID, kickoff, sddName, headless, quiet, sendNotify, out); err != nil {
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
	fn func(issue, slug, agentName, sddName, agentSuffix string, headless, quiet, sendNotify bool, out io.Writer) error,
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
			err := fn(iss, "", agentName, sddName, "", true, quiet, sendNotify, &buf)
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

// runMultiAgent starts multiple agents on the same issue concurrently, each in
// its own worktree with an agent-name suffix (e.g. repo-42-slug-claude).
// All agents always run in headless mode; foreground is not supported here.
func runMultiAgent(issue string, agents []string, sddName string, quiet, sendNotify bool, out, errOut io.Writer) error {
	// Validate all adapters and resolve (possibly clone) the repo once before
	// spawning goroutines. This prevents concurrent clone races when issue is a
	// URL, and surfaces adapter/token errors early.
	for _, ag := range agents {
		if err := validateAdapter(ag); err != nil {
			return err
		}
		if err := validateAgentSuffix(ag); err != nil {
			return err
		}
	}
	_, _, _, p, err := repoRootForIssue(issue, out)
	if err != nil {
		return err
	}
	if err := p.AuthCheck(); err != nil {
		return err
	}

	type agentResult struct {
		agent  string
		output string
		err    error
	}
	results := make([]agentResult, len(agents))

	var wg sync.WaitGroup
	for i, ag := range agents {
		wg.Add(1)
		go func(i int, ag string) {
			defer wg.Done()
			var buf strings.Builder
			err := startOne(issue, "", ag, sddName, ag, true, quiet, sendNotify, &buf)
			results[i] = agentResult{agent: ag, output: buf.String(), err: err}
		}(i, ag)
	}
	wg.Wait()

	var hasErr bool
	for _, r := range results {
		fmt.Fprint(out, r.output)
		if r.err != nil {
			fmt.Fprintf(errOut, "[%s] error: %v\n", r.agent, r.err)
			hasErr = true
		}
	}
	if hasErr {
		return fmt.Errorf("one or more agents failed to start")
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
		headless    bool
		quiet       bool
		sendNotify  bool
		agentSuffix string
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
			return runReleasePausedSession(args[0], feedback, agentSuffix, headless, quiet, sendNotify)
		},
	}
	c.Flags().BoolVar(&headless, "headless", false, "Run agent in background (log -> agent.log)")
	c.Flags().BoolVar(&quiet, "quiet", false, "Suppress agent log output; show spinner/heartbeat only")
	c.Flags().BoolVar(&sendNotify, "notify", false, "Send a desktop notification when the headless agent finishes")
	c.Flags().StringVar(&agentSuffix, "agent", "", "Agent name to disambiguate when multiple worktrees exist for the same issue")
	return c
}

func runReleasePausedSession(issue, feedback, agentSuffix string, headless, quiet, sendNotify bool) error {
	// Accept full GitHub issue URLs; extract the bare number for local lookup.
	if _, _, num, ok := parseIssueURL(issue); ok {
		issue = num
	}

	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}

	// Combine --notify flag with the per-repo config setting (either enables it).
	// notify: true in .agentctl.yml is only meaningful in headless mode.
	if !sendNotify {
		if cfg, cfgErr := config.ReadMerged(repoRoot); cfgErr == nil && cfg.Notify != nil && *cfg.Notify {
			sendNotify = true
		}
	}

	wt, err := resolveWorktree(repoRoot, issue, agentSuffix)
	if err != nil {
		return err
	}

	af, err := state.Read(wt.Path)
	if err != nil || af.Agent == "" {
		return fmt.Errorf("no .agent file for issue %s; cannot resume non-interactively.\nUse 'cd %s && %s --resume' instead.", issue, wt.Path, af.Agent)
	}

	if err := validateAdapter(af.Agent); err != nil {
		return err
	}

	// For SDD runs, require the spec pause to have been reached before resuming.
	if af.SDD != "" && computeSpecState(wt.Path, effectiveSpecKey(issue, af), af.SDD, af.SDDSet) == "no-spec" {
		return fmt.Errorf("spec not yet generated for issue %s; paused state not reached.\nTail %s/agent.log to confirm and retry once the pause is reported.", issue, wt.Path)
	}

	specPath := findSpecPath(wt.Path, effectiveSpecKey(issue, af))
	prompt := buildResumePrompt(feedback, af.SDD, specPath)
	if af.SDD != "" && strings.TrimSpace(feedback) == "" {
		if err := state.AppendKey(wt.Path, "sdd-stage", "2"); err != nil {
			return fmt.Errorf("persist sdd stage for issue %s: %w", issue, err)
		}
	}
	return agentResume(af.Agent, wt.Path, issue, af.SessionID, prompt, headless, quiet, sendNotify)
}

// ─── discard ──────────────────────────────────────────────────────────────────

// NewDiscardCmd creates the `discard` subcommand.
func NewDiscardCmd() *cobra.Command {
	var stale bool
	var agentSuffix string
	c := &cobra.Command{
		Use:   "discard [issue[,issue...]]",
		Short: "Permanently delete a worktree and its local/remote branches",
		Long: `Discard the worktree for an issue and delete the local and remote branches.
This action is NOT recoverable. You will be prompted to type YES to confirm.

If no issue number is given, it is inferred from the current branch when
you are inside a linked worktree.

Multiple issues may be given as a comma-separated list. Each list item may
be a bare issue number or a full GitHub issue URL, e.g.:

  agentctl discard 55,56,57
  agentctl discard 55,https://github.com/org/repo/issues/56,57

Each worktree is discarded in sequence; you will be prompted to confirm
each one.

Use --stale to discard all worktrees that have no running agent and no PR.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if stale {
				if len(args) > 0 {
					return fmt.Errorf("--stale and an issue number are mutually exclusive")
				}
				return runDiscardStale()
			}
			if len(args) == 0 {
				issue, err := resolveIssueArg("discard", nil)
				if err != nil {
					return err
				}
				return runRemoveWorktree(issue, agentSuffix)
			}
			rawIssues := strings.Split(args[0], ",")
			issues := make([]string, 0, len(rawIssues))
			for _, iss := range rawIssues {
				if s := strings.TrimSpace(iss); s != "" {
					if _, _, num, ok := parseIssueURL(s); ok {
						s = num
					}
					issues = append(issues, s)
				}
			}
			if len(issues) == 0 {
				return fmt.Errorf("no valid issue tokens found in %q", args[0])
			}
			for _, issue := range issues {
				if err := runRemoveWorktree(issue, agentSuffix); err != nil {
					return err
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&stale, "stale", false, "Discard all worktrees with no running agent and no PR")
	c.Flags().StringVar(&agentSuffix, "agent", "", "Agent name to target a specific worktree when multiple agents ran on the same issue")
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

// isStaleWorktree returns true when no agent is running and no PR/MR of any state
// exists for the branch. Returns false conservatively when PR status cannot be
// reliably determined (e.g. auth or network failures) to avoid discarding
// potentially active worktrees.
func isStaleWorktree(repoRoot, branch, wtPath string) bool {
	if isAgentRunning(wtPath) {
		return false
	}
	p, err := resolveProvider(repoRoot)
	if err != nil {
		return false
	}
	hasPR, err := p.HasPR(repoRoot, branch)
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

func runRemoveWorktree(issue, agentSuffix string) error {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}

	var wtPath, branch string
	wt, resolveErr := resolveWorktree(repoRoot, issue, agentSuffix)
	if resolveErr == nil {
		wtPath = wt.Path
		branch = wt.Branch
	} else if errors.Is(resolveErr, errAmbiguousWorktree) {
		return resolveErr
	} else if !errors.Is(resolveErr, errWorktreeNotFound) {
		// Real error (e.g. git worktree list failure) — don't silently fall through.
		return resolveErr
	}

	// If no registered worktree, try to find a local branch.
	if branch == "" && isNumericIssue(issue) {
		branch, _ = git.FindBranchByIssuePrefix(repoRoot, issue)
	}
	if branch == "" && !isNumericIssue(issue) && git.BranchExists(repoRoot, issue) {
		branch = issue
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
		// Write diagnostics before removing the worktree (run-file may be inside it).
		finaliseDiagnostics(repoRoot, wtPath, af, "discarded")
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

// ─── merge ────────────────────────────────────────────────────────────────────

// NewMergeCmd creates the `merge` subcommand.
// It merges an approved PR via gh and then performs worktree cleanup.
func NewMergeCmd() *cobra.Command {
	var strategy string
	var noDelete bool
	var dryRun bool
	var agentSuffix string
	c := &cobra.Command{
		Use:   "merge [issue]",
		Short: "Merge an approved PR and clean up the worktree",
		Long: `One-step merge: verify the PR is mergeable, run gh pr merge, pull main,
and (unless --no-delete) remove the worktree and branches.

Run without arguments inside a linked worktree to infer the issue number
from the current branch.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssueArg("merge", args)
			if err != nil {
				return err
			}
			return runMerge(issue, strategy, agentSuffix, noDelete, dryRun)
		},
	}
	c.Flags().StringVar(&strategy, "strategy", "", "Merge strategy: squash (default), merge, or rebase")
	c.Flags().BoolVar(&noDelete, "no-delete", false, "Skip worktree deletion after merge")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would happen without doing it")
	c.Flags().StringVar(&agentSuffix, "agent", "", "Agent name to target a specific worktree when multiple agents ran on the same issue")
	return c
}

// resolveMergeStrategy returns the effective strategy: flag > config > "squash".
func resolveMergeStrategy(flag, cfgStrategy string) string {
	if flag != "" {
		return flag
	}
	if cfgStrategy != "" {
		return cfgStrategy
	}
	return "squash"
}

// validateMergeStrategy returns an error for unrecognised strategy names.
func validateMergeStrategy(s string) error {
	switch s {
	case "squash", "merge", "rebase":
		return nil
	default:
		return fmt.Errorf("unknown merge strategy %q: must be squash, merge, or rebase", s)
	}
}

func runMerge(issue, strategy, agentSuffix string, noDelete, dryRun bool) error {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}

	cfg, err := config.ReadMerged(repoRoot)
	if err != nil {
		return fmt.Errorf("cannot read config: %w", err)
	}

	strategy = resolveMergeStrategy(strategy, cfg.MergeStrategy)
	if err := validateMergeStrategy(strategy); err != nil {
		return err
	}

	// Locate the worktree and branch for the issue.
	var branch string
	wt, resolveErr := resolveWorktree(repoRoot, issue, agentSuffix)
	if resolveErr == nil {
		if wt.Branch != "" && wt.Branch != "HEAD" {
			branch = wt.Branch
		} else {
			branch, err = git.CurrentBranch(wt.Path)
			if err != nil || branch == "" || branch == "HEAD" {
				return fmt.Errorf("could not determine branch for %s", wt.Path)
			}
		}
	} else if errors.Is(resolveErr, errAmbiguousWorktree) {
		return resolveErr
	} else if !errors.Is(resolveErr, errWorktreeNotFound) {
		return resolveErr
	} else {
		branch, _ = git.FindBranchByIssuePrefix(repoRoot, issue)
		if branch == "" {
			return fmt.Errorf("no worktree or local branch found for issue %s", issue)
		}
	}

	p, err := resolveProvider(repoRoot)
	if err != nil {
		return fmt.Errorf("cannot detect VCS provider: %w", err)
	}

	// Validate the PR/MR is open and not conflicting.
	prState, mergeable, reviewDecision, err := p.PRMergeInfo(repoRoot, branch)
	if err != nil {
		return fmt.Errorf("could not determine %s state for %s: %w\nIs %s installed and authenticated?", p.PRTerm(), branch, err, p.CLI())
	}
	switch prState {
	case "MERGED":
		return fmt.Errorf("%s for %s is already MERGED — use: agentctl cleanup %s", p.PRTerm(), branch, issue)
	case "CLOSED":
		return fmt.Errorf("%s for %s is CLOSED and cannot be merged", p.PRTerm(), branch)
	case "":
		return fmt.Errorf("no open %s found for branch %s", p.PRTerm(), branch)
	}
	if mergeable == "CONFLICTING" {
		return fmt.Errorf("%s for %s has merge conflicts; resolve them before merging", p.PRTerm(), branch)
	}
	if reviewDecision == "CHANGES_REQUESTED" {
		return fmt.Errorf("%s for %s has changes requested; resolve review feedback before merging", p.PRTerm(), branch)
	}

	if dryRun {
		fmt.Printf("[dry-run] Would merge %s for issue %s (branch: %s, strategy: %s)\n", p.PRTerm(), issue, branch, strategy)
		if !noDelete {
			fmt.Printf("[dry-run] Would pull main and remove worktree after merge\n")
		} else {
			fmt.Printf("[dry-run] Would pull main (--no-delete: worktree kept)\n")
		}
		return nil
	}

	fmt.Printf("Merging %s for issue %s (branch: %s, strategy: %s)...\n", p.PRTerm(), issue, branch, strategy)
	if err := p.MergePR(repoRoot, branch, strategy); err != nil {
		return fmt.Errorf("%s %s merge failed: %w", p.CLI(), p.PRTerm(), err)
	}
	fmt.Printf("%s merged.\n", p.PRTerm())

	if noDelete {
		currentBranch, err := git.CurrentBranch(repoRoot)
		if err != nil {
			return err
		}
		if currentBranch != "main" {
			fmt.Printf("Primary worktree at %s is on '%s'; checking out main...\n", repoRoot, currentBranch)
			if err := git.CheckoutMain(repoRoot); err != nil {
				return fmt.Errorf("cannot check out main in %s: %w", repoRoot, err)
			}
		}
		fmt.Printf("Pulling main in %s ...\n", repoRoot)
		return git.PullFFOnly(repoRoot)
	}

	return cleanupMerged(repoRoot, issue, agentSuffix)
}

// ─── cleanup-merged ───────────────────────────────────────────────────────────

// NewCleanupMergedCmd creates the `cleanup-merged` subcommand.
// NewCleanupCmd creates the `cleanup` subcommand.
// With --all it sweeps all merged worktrees; otherwise it cleans up one or more issues.
func NewCleanupCmd() *cobra.Command {
	var all bool
	var agentSuffix string
	c := &cobra.Command{
		Use:   "cleanup [issue[,issue...]]...",
		Short: "Remove a merged worktree and its branches",
		Long: `Post-merge cleanup: pull main, remove the worktree, and delete the local
and remote branches.

Run without arguments inside a linked worktree to infer the issue number
from the current branch.

Multiple issues may be given as a comma-separated list or as separate
arguments (e.g. "agentctl cleanup 240,277" or "agentctl cleanup 240 277").
Each issue is cleaned up in sequence; all failures are reported at the end.

Use --all to sweep every linked worktree whose PR is MERGED in one pass.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all {
				if len(args) > 0 {
					return fmt.Errorf("--all and issue arguments are mutually exclusive")
				}
				return runCleanupAllMerged()
			}
			if len(args) == 0 {
				// No args: infer from current worktree branch.
				issue, err := resolveIssueArg("cleanup", args)
				if err != nil {
					return err
				}
				return runCleanupMerged(issue, agentSuffix)
			}
			issues := parseCleanupIssues(args)
			if len(issues) == 0 {
				return fmt.Errorf("no valid issue tokens found in %q", strings.Join(args, " "))
			}
			if len(issues) == 1 {
				return runCleanupMerged(issues[0], agentSuffix)
			}
			var errs []string
			for _, iss := range issues {
				if err := runCleanupMerged(iss, agentSuffix); err != nil {
					fmt.Fprintf(os.Stderr, "cleanup %s: %v\n", iss, err)
					errs = append(errs, iss)
				}
			}
			if len(errs) > 0 {
				return fmt.Errorf("%d cleanup(s) failed: %s", len(errs), strings.Join(errs, ", "))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&all, "all", false, "Clean up all worktrees whose PR is MERGED")
	c.Flags().StringVar(&agentSuffix, "agent", "", "Agent name to target a specific worktree when multiple agents ran on the same issue")
	return c
}

// parseCleanupIssues expands a mix of space-separated args and comma-separated
// tokens into a flat, trimmed list of issue identifiers.
func parseCleanupIssues(args []string) []string {
	var issues []string
	for _, arg := range args {
		for _, tok := range strings.Split(arg, ",") {
			if s := strings.TrimSpace(tok); s != "" {
				if _, _, num, ok := parseIssueURL(s); ok {
					s = num
				}
				issues = append(issues, s)
			}
		}
	}
	return issues
}

func runCleanupMerged(issue, agentSuffix string) error {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}
	return cleanupMerged(repoRoot, issue, agentSuffix)
}

func cleanupMerged(repoRoot, issue, agentSuffix string) error {
	var wtPath, branch string
	var wtRegistered bool

	wt, resolveErr := resolveWorktree(repoRoot, issue, agentSuffix)
	if resolveErr == nil {
		wtRegistered = true
		wtPath = wt.Path
		var branchErr error
		branch, branchErr = git.CurrentBranch(wtPath)
		if branchErr != nil || branch == "" || branch == "HEAD" {
			return fmt.Errorf("could not determine branch for %s", wtPath)
		}
	} else if errors.Is(resolveErr, errAmbiguousWorktree) {
		return resolveErr
	} else if !errors.Is(resolveErr, errWorktreeNotFound) {
		// Real error (e.g. git worktree list failure) — don't silently fall through.
		return resolveErr
	} else {
		// Recovery path: worktree is no longer registered but branches may exist.
		if isNumericIssue(issue) {
			branch, _ = git.FindBranchByIssuePrefix(repoRoot, issue)
		}
		if branch == "" && !isNumericIssue(issue) && git.BranchExists(repoRoot, issue) {
			branch = issue
		}
		if branch == "" {
			return fmt.Errorf("no worktree or local branch found for issue %s", issue)
		}
		repoName := filepath.Base(repoRoot)
		parentDir := filepath.Dir(repoRoot)
		candidate := filepath.Join(parentDir, repoName+"-"+strings.ReplaceAll(branch, "/", "-"))
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

	// Verify merge via VCS CLI.
	p, detErr := resolveProvider(repoRoot)
	if detErr != nil {
		return fmt.Errorf("cannot detect VCS provider: %w", detErr)
	}
	prState, _, _, prErr := p.PRForBranch(repoRoot, branch)
	if prErr != nil {
		return fmt.Errorf("could not determine %s state for %s.\nIs %s installed and authenticated? If this branch has no %s, use:\n  agentctl discard %s", p.PRTerm(), branch, p.CLI(), p.PRTerm(), issue)
	}
	if prState != "MERGED" {
		return fmt.Errorf("%s for %s is %s, not MERGED.\nUse: agentctl discard %s", p.PRTerm(), branch, prState, issue)
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
				if err2 := removeAllWritable(wtPath); err2 != nil {
					return err2
				}
			}
		} else if _, statErr := os.Stat(wtPath); statErr == nil {
			if err := removeAllWritable(wtPath); err != nil {
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

// removeAllWritable removes the directory tree at path, first making all
// entries writable. This is required when .agent-home contains a Go module
// cache (go/pkg/mod) whose files are intentionally read-only (0444).
// Symlinks are skipped during chmod to avoid modifying permissions on targets
// outside the worktree (e.g. ~/.ssh, ~/.gitconfig linked into .agent-home).
func removeAllWritable(path string) error {
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		_ = os.Chmod(p, 0o700)
		return nil
	})
	return os.RemoveAll(path)
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

	p, err := resolveProvider(repoRoot)
	if err != nil {
		return fmt.Errorf("cannot detect VCS provider: %w", err)
	}

	cleaned, skipped, failed, staleCount := 0, 0, 0, 0
	handledBranches := make(map[string]struct{}, len(wts))
	for _, wt := range wts {
		branch := wt.Branch
		if branch == "" || branch == "HEAD" {
			fmt.Printf("Skipping %s: detached HEAD or cannot determine branch\n", wt.Path)
			skipped++
			continue
		}
		handledBranches[branch] = struct{}{}
		prState, _, _, err := p.PRForBranch(repoRoot, branch)
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
		// Derive agent suffix from path+.agent so multi-agent worktrees are
		// cleaned up correctly without triggering the ambiguity error.
		agentSuffix := ""
		if af, afErr := state.Read(wt.Path); afErr == nil && af.Agent != "" {
			if strings.HasSuffix(wt.Path, "-"+af.Agent) {
				agentSuffix = af.Agent
			}
		}
		fmt.Printf("--- Cleaning issue %s (%s) ---\n", issue, branch)
		if err := cleanupMerged(repoRoot, issue, agentSuffix); err != nil {
			fmt.Fprintf(os.Stderr, "FAILED to clean issue %s (%s): %v\n", issue, branch, err)
			failed++
		} else {
			cleaned++
		}
	}

	orphanedPruned := 0
	remoteBranches, err := git.ListRemoteBranches(repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not list remote branches: %v\n", err)
	} else {
		for _, branch := range remoteBranches {
			if branch == "main" || branch == "master" {
				continue
			}
			if _, ok := handledBranches[branch]; ok {
				continue
			}
			if git.InferIssue(branch) == "" {
				continue
			}

			prState, _, _, err := p.PRForBranch(repoRoot, branch)
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: could not get %s state for remote branch %s: %v\n", p.PRTerm(), branch, err)
				failed++
				continue
			}
			if prState == "" {
				fmt.Printf("Skipping orphaned remote branch %s: no %s found\n", branch, p.PRTerm())
				skipped++
				continue
			}
			if prState != "MERGED" && prState != "CLOSED" {
				fmt.Printf("Skipping orphaned remote branch %s: %s is %s\n", branch, p.PRTerm(), prState)
				skipped++
				continue
			}

			fmt.Printf("Pruning orphaned remote branch %s (PR %s, no local worktree)\n", branch, prState)
			msg, err := git.DeleteRemoteBranch(repoRoot, branch)
			if err != nil {
				if strings.Contains(msg, "remote ref does not exist") {
					continue
				}
				fmt.Fprintf(os.Stderr, "WARNING: could not delete remote branch %s: %s\n", branch, strings.TrimSpace(msg))
				failed++
				continue
			}

			fmt.Printf("Deleted remote branch origin/%s\n", branch)
			orphanedPruned++
		}
	}

	fmt.Printf("\n%d merged worktrees cleaned, %d orphaned remote branches pruned, %d skipped\n", cleaned, orphanedPruned, skipped)
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
	var asJSON bool
	c := &cobra.Command{
		Use:     "status",
		Aliases: []string{"list"},
		Short:   "Show status table for all linked worktrees",
		Long: `Print a status table of every linked worktree provisioned by agentctl.

Compact (default):  ISSUE  BRANCH  AGENT  PORT  SPEC  PR
Verbose:            ISSUE  BRANCH  AGENT  PATH  PORT  DEV-PID  AGENT-PID  SPEC  PR  SESSION

Use --json to emit a JSON array of run records from .agentctl/runs/.
Each entry includes issue, branch, agent, status,
started_at, elapsed_seconds, pr_url, files_changed, and tokens_used.

Spec states:  no-spec | paused | in-progress | done
PR states:    none | OPEN | MERGED | CLOSED`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON {
				return runStatusJSON()
			}
			return runStatus(verbose)
		},
	}
	c.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show full table including PATH, PIDs, and SESSION")
	c.Flags().BoolVar(&asJSON, "json", false, "Emit JSON array of run records from .agentctl/runs/")
	return c
}

func runStatus(verbose bool) error {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	return runStatusTable(repoRoot, verbose, w)
}

// runStatusTable writes the status table for all agentctl-managed worktrees in
// repoRoot to out. Worktrees with no .agent file (e.g. created manually with
// git worktree add) are silently skipped.
func runStatusTable(repoRoot string, verbose bool, out io.Writer) error {
	wts, err := git.LinkedWorktrees(repoRoot)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if verbose {
		fmt.Fprintln(w, "ISSUE\tBRANCH\tAGENT\tPATH\tPORT\tDEV-PID\tAGENT-PID\tSPEC\tPR\tSESSION")
	} else {
		fmt.Fprintln(w, "ISSUE\tBRANCH\tAGENT\tPORT\tSPEC\tPR")
	}

	for _, wt := range wts {
		af, _ := state.Read(wt.Path)
		if af.Agent == "" {
			continue // skip worktrees not managed by agentctl
		}

		issue := wt.Issue
		if issue == "" {
			issue = "-"
		}
		branch := wt.Branch
		if branch == "" {
			branch = "?"
		}

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
		specState := computeSpecState(wt.Path, worktreeSpecLookupKey(wt, af), af.SDD, af.SDDSet)

		prState := "none"
		if branch != "?" && branch != "HEAD" {
			statusProvider, provErr := resolveProvider(repoRoot)
			if provErr != nil {
				fmt.Fprintf(os.Stderr, "WARNING: could not detect VCS provider for %s: %v\n", repoRoot, provErr)
			} else if af.PRNumber != "" {
				// Cache hit — use stored number, call VCS CLI live for current state.
				if ps, _, prURL, err := statusProvider.PRForBranch(repoRoot, af.PRNumber); err == nil && ps != "" {
					prState = fmt.Sprintf("#%s %s", af.PRNumber, ps)
					// Backfill pr-url if it was missing (partial write or manually edited .agent).
					if af.PRURL == "" && prURL != "" {
						if appendErr := state.AppendKeyIfExists(wt.Path, "pr-url", prURL); appendErr != nil {
							fmt.Fprintf(os.Stderr, "WARNING: could not cache pr-url: %v\n", appendErr)
						}
					}
				}
			} else {
				// Cache miss — discover PR/MR via branch name.
				if ps, n, prURL, err := statusProvider.PRForBranch(repoRoot, branch); err == nil && ps != "" {
					if n > 0 {
						prState = fmt.Sprintf("#%d %s", n, ps)
						if appendErr := state.AppendKeyIfExists(wt.Path, "pr", strconv.Itoa(n)); appendErr != nil {
							fmt.Fprintf(os.Stderr, "WARNING: could not cache pr: %v\n", appendErr)
						}
						if appendErr := state.AppendKeyIfExists(wt.Path, "pr-url", prURL); appendErr != nil {
							fmt.Fprintf(os.Stderr, "WARNING: could not cache pr-url: %v\n", appendErr)
						}
					} else {
						prState = ps
					}
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

// runStatusJSON emits a JSON array of run records from .agentctl/runs/,
// sorted by started_at descending (newest first).
func runStatusJSON() error {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}
	records, err := diagnostics.List(repoRoot)
	if err != nil {
		return fmt.Errorf("reading run records: %w", err)
	}
	// Reverse to show newest first.
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}
	// statusJSON is a JSON-friendly projection of RunRecord with a "status" alias.
	type statusJSON struct {
		Issue          string     `json:"issue"`
		Branch         string     `json:"branch"`
		Agent          string     `json:"agent"`
		Status         string     `json:"status"`
		StartedAt      time.Time  `json:"started_at"`
		StoppedAt      *time.Time `json:"stopped_at,omitempty"`
		ElapsedSeconds float64    `json:"elapsed_seconds,omitempty"`
		PRURL          string     `json:"pr_url,omitempty"`
		FilesChanged   int        `json:"files_changed,omitempty"`
		TokensUsed     int64      `json:"tokens_used,omitempty"`
	}
	out := make([]statusJSON, 0, len(records))
	for _, r := range records {
		out = append(out, statusJSON{
			Issue:          r.Issue,
			Branch:         r.Branch,
			Agent:          r.Agent,
			Status:         r.ExitReason,
			StartedAt:      r.StartedAt,
			StoppedAt:      r.StoppedAt,
			ElapsedSeconds: r.ElapsedSeconds,
			PRURL:          r.PRURL,
			FilesChanged:   r.FilesChanged,
			TokensUsed:     r.TokensUsed,
		})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// ─── logs ─────────────────────────────────────────────────────────────────────

// NewLogsCmd creates the `logs` subcommand.
func NewLogsCmd() *cobra.Command {
	var (
		lines       int
		noFollow    bool
		agentSuffix string
	)
	c := &cobra.Command{
		Use:   "logs <issue>",
		Short: "Stream agent.log; follows new output by default",
		Long: `Stream agent.log for the given issue to stdout.

By default the last 50 lines are printed and new output is followed until
Ctrl+C. Use --no-follow to print history and exit immediately.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(args[0], lines, noFollow, agentSuffix, os.Stdout)
		},
	}
	c.Flags().IntVar(&lines, "lines", 50, "Lines of history to show before following")
	c.Flags().BoolVar(&noFollow, "no-follow", false, "Print history and exit without following")
	c.Flags().StringVar(&agentSuffix, "agent", "", "Agent name to disambiguate when multiple worktrees exist for the same issue")
	return c
}

// runLogs resolves the worktree for issue and streams its agent.log.
func runLogs(issue string, lines int, noFollow bool, agentSuffix string, w io.Writer) error {
	if _, _, num, ok := parseIssueURL(issue); ok {
		issue = num
	}
	wtPath, err := findWorktreePath(issue, agentSuffix)
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
				if af2.DevPort != "" {
					fmt.Fprintf(w, "\nDev server: http://localhost:%s\n", af2.DevPort)
				}
				id := af2.IssueArg
				if id == "" {
					id = issue
				}
				branch2, _ := git.CurrentBranch(wtPath)
				specPath := findSpecPath(wtPath, issue)
				switch {
				case af2.SDD != "" && af2.SDDStage < 2 && specPath != "":
					fmt.Fprintf(w, "Spec: %s\nSpec ready for review — agentctl resume %s\n", specPath, id)
				case af2.SDD != "" && af2.SDDStage >= 2:
					if reportPRStatus(w, wtPath, branch2, issue, false) {
						fmt.Fprintf(w, "agentctl cleanup %s   # after PR is merged\n", id)
					} else {
						fmt.Fprintf(w, "agent process has exited\n")
					}
				default:
					fmt.Fprintf(w, "agent process has exited\n")
				}
				// Update diagnostics: determine exit reason and PR URL.
				if repoRoot2 := repoRootFromWorktree(wtPath); repoRoot2 != "" {
					exitReason := "failed"
					prURL := af2.PRURL
					if prURL == "" && branch2 != "" {
						if p2, pErr := resolveProvider(repoRoot2); pErr == nil {
							if _, _, foundURL, pErr := p2.PRForBranch(repoRoot2, branch2); pErr == nil && foundURL != "" {
								prURL = foundURL
							}
						}
					}
					if prURL != "" {
						exitReason = "pr_opened"
					}
					af2.PRURL = prURL
					finaliseDiagnostics(repoRoot2, wtPath, af2, exitReason)
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
	var agentSuffix string
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
			wtPath, err := findWorktreePath(issue, agentSuffix)
			if err != nil {
				return err
			}
			return runDevStart(wtPath, quiet, os.Stdout)
		},
	}
	c.Flags().BoolVar(&quiet, "quiet", false, "Suppress log streaming; only print the ready URL")
	c.Flags().StringVar(&agentSuffix, "agent", "", "Agent name to disambiguate when multiple worktrees exist for the same issue")
	return c
}

// runDevStart launches the dev server in wtPath using the port already
// recorded in the .agent state file. It updates dev-pid in .agent after
// a successful launch.
func runDevStart(wtPath string, quiet bool, out io.Writer) error {
	cfg, err := config.ReadMerged(wtPath)
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

	if cfg.Notify != nil && *cfg.Notify {
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
	detachProcess(devCmd)
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
	var agentSuffix string
	c := &cobra.Command{
		Use:   "attach <issue>",
		Short: "Follow a running headless agent and exit when it finishes",
		Long: `Attach to a running headless agent: stream agent.log to stdout and exit
automatically when the agent process ends.

If the agent has already finished, the last 50 lines of agent.log are printed
and the command exits with "agent has already finished".

Press Ctrl+C to detach without stopping the agent.`,
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
			return attachLog(wtPath, arg, os.Stdout, 10*time.Second)
		},
	}
	c.Flags().StringVar(&agentSuffix, "agent", "", "Agent name to disambiguate when multiple worktrees exist for the same issue")
	return c
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
			reportPRStatus(w, wtPath, branch, issue, false)
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
		reportPRStatus(w, wtPath, branch, issue, false)
	}
	return nil
}

// NewDiffCmd creates the `diff` subcommand.
func NewDiffCmd() *cobra.Command {
	var stat bool
	var base string
	var noPager bool
	var agentSuffix string
	c := &cobra.Command{
		Use:   "diff <issue>",
		Short: "Show what the agent has changed in a worktree",
		Long: `Show the git diff for the agent's worktree.

By default prints all uncommitted changes (staged + unstaged) and pipes the
output through the configured pager ($PAGER, defaulting to less -R).

Use --base to compare against a branch, e.g. --base main shows everything the
agent added since branching from main.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(args[0], base, agentSuffix, stat, noPager)
		},
	}
	c.Flags().BoolVar(&stat, "stat", false, "Show changed-file summary instead of full diff")
	c.Flags().StringVar(&base, "base", "", "Diff base branch (default: show uncommitted changes)")
	c.Flags().BoolVar(&noPager, "no-pager", false, "Print to stdout without paging")
	c.Flags().StringVar(&agentSuffix, "agent", "", "Agent name to disambiguate when multiple worktrees exist for the same issue")
	return c
}

func runDiff(issue, base, agentSuffix string, stat, noPager bool) error {
	if _, _, num, ok := parseIssueURL(issue); ok {
		issue = num
	}
	wtPath, err := findWorktreePath(issue, agentSuffix)
	if err != nil {
		return err
	}

	// Use --color=always only when piping to the pager: git detects a pipe
	// and disables colour on its own, but we want colour in the pager.
	// For all other paths (--no-pager, --stat, non-TTY) let git auto-detect.
	usePager := !noPager && !stat && isTerminal(os.Stdout)
	colorFlag := "--color=auto"
	if usePager {
		colorFlag = "--color=always"
	}

	args := []string{"diff", colorFlag}
	if stat {
		args = append(args, "--stat")
	}
	if base != "" {
		args = append(args, base+"...HEAD")
	} else {
		args = append(args, "HEAD")
	}

	gitCmd := exec.Command("git", args...)
	gitCmd.Dir = wtPath
	gitCmd.Stderr = os.Stderr

	if !usePager {
		gitCmd.Stdout = os.Stdout
		return gitCmd.Run()
	}

	// Build the pager command.  When $PAGER is set, invoke it via "sh -c" so
	// complex values like "less -FRSX" or "delta" are handled correctly.
	// When $PAGER is unset, default to "less -R".
	var pagerCmd *exec.Cmd
	if pagerEnv := os.Getenv("PAGER"); pagerEnv != "" {
		pagerCmd = exec.Command("sh", "-c", pagerEnv)
	} else {
		pagerCmd = exec.Command("less", "-R")
	}

	pipe, err := gitCmd.StdoutPipe()
	if err != nil {
		gitCmd.Stdout = os.Stdout
		return gitCmd.Run()
	}
	pagerCmd.Stdin = pipe
	pagerCmd.Stdout = os.Stdout
	pagerCmd.Stderr = os.Stderr
	if err := pagerCmd.Start(); err != nil {
		gitCmd.Stdout = os.Stdout
		return gitCmd.Run()
	}
	gitErr := gitCmd.Run()
	pagerErr := pagerCmd.Wait()
	// git exits with SIGPIPE when the pager quits early (user pressed 'q');
	// treat that as success and surface the pager exit status instead.
	if gitErr != nil && !isSignalError(gitErr, syscall.SIGPIPE) {
		return gitErr
	}
	return pagerErr
}

// isSignalError reports whether err is an *exec.ExitError caused by the given
// Unix signal.  Returns false on non-Unix platforms or when err is not an
// ExitError.
func isSignalError(err error, sig syscall.Signal) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return ws.Signaled() && ws.Signal() == sig
		}
	}
	return false
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
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
	// New worktree explicitly created without --sdd.
	if sddSet && sddName == "" {
		return "no-spec"
	}
	// Plain-style: flat specs/spec.md with no lifecycle subdirectory.
	if _, err := os.Stat(filepath.Join(wtPath, "specs", "spec.md")); err == nil {
		return "paused"
	}
	if issue == "" {
		return "no-spec"
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
	if issue == "" {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(wtPath, "specs", issue+"-*", "spec.md"))
	if len(matches) > 0 {
		return matches[0]
	}
	return ""
}

func isNumericIssue(issue string) bool {
	if issue == "" {
		return false
	}
	_, err := strconv.Atoi(issue)
	return err == nil
}

func specLookupKey(issue string) string {
	if strings.HasPrefix(issue, "task/") {
		return "task"
	}
	return issue
}

// effectiveSpecKey returns the spec lookup key for a worktree, using the
// task-mode flag from the .agent file when available. If the worktree was
// started with `agentctl start --task`, the key is always "task" regardless
// of the branch name (specs are stored under the "task" key in task mode).
func effectiveSpecKey(issue string, af state.AgentFile) string {
	if af.TaskMode {
		return "task"
	}
	return specLookupKey(issue)
}

func worktreeSpecLookupKey(wt git.Worktree, af state.AgentFile) string {
	if af.TaskMode {
		return "task"
	}
	if wt.Issue != "" {
		return wt.Issue
	}
	return specLookupKey(wt.Branch)
}

func findLinkedWorktree(repoRoot, ref string) (git.Worktree, bool, error) {
	if isNumericIssue(ref) {
		wt, found, err := git.FindWorktreeByIssue(repoRoot, ref)
		if err != nil || found {
			return wt, found, err
		}
	}
	return git.FindWorktreeByBranch(repoRoot, ref)
}

type prInfo struct {
	Number int    `json:"number"`
	Body   string `json:"body"`
	URL    string `json:"url"`
}

// linkPRToIssue appends "Closes #<issueNum>" to the open PR/MR for branch,
// unless the body already contains a closing keyword (closes/fixes).
// Returns the PR info (including URL) so callers can display it.
// Silently returns nil, nil when no PR/MR exists.
func linkPRToIssue(dir, branch, issueNum string) (*prInfo, error) {
	p, err := resolveProvider(dir)
	if err != nil {
		return nil, fmt.Errorf("could not detect VCS provider: %w", err)
	}

	prState, number, url, err := p.PRForBranch(dir, branch)
	if err != nil {
		// "no pull request(s) found" or equivalent — not an error.
		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "no pull request") || strings.Contains(errMsg, "no merge request") || strings.Contains(errMsg, "404") {
			return nil, nil
		}
		return nil, fmt.Errorf("%s pr view: %w", p.CLI(), err)
	}
	if number == 0 || prState == "" {
		return nil, nil
	}

	// We don't have the body from PRForBranch, so construct a minimal prInfo.
	pr := &prInfo{Number: number, URL: url}

	if !isNumericIssue(issueNum) || prState != "OPEN" {
		return pr, nil
	}

	// Re-fetch the PR/MR details to get the body so we can append a closing
	// keyword when it is missing.  Errors here are surfaced rather than
	// swallowed so regressions in issue-linking are immediately visible.
	fullNumber, body, fullURL, detailErr := p.FetchPRDetails(dir, branch)
	if detailErr != nil {
		return pr, fmt.Errorf("%s pr view: %w", p.CLI(), detailErr)
	}
	full := &prInfo{Number: fullNumber, Body: body, URL: fullURL}
	lower := strings.ToLower(body)
	if !strings.Contains(lower, "closes #"+issueNum) && !strings.Contains(lower, "fixes #"+issueNum) {
		newBody := strings.TrimRight(body, "\n") + "\n\nCloses #" + issueNum
		if editErr := p.EditPRBody(dir, fullNumber, newBody); editErr != nil {
			return full, fmt.Errorf("%s pr edit: %w", p.CLI(), editErr)
		}
	}
	return full, nil
}

// reportPRStatus links the PR to the issue and prints the PR URL to w.
// Returns true when a PR was found. When quietOnNone is false it prints
// "PR: none" when no PR exists; when true it stays silent on no-PR so
// callers at spec-review stage don't show a misleading "PR: none".
func reportPRStatus(w io.Writer, dir, branch, issueNum string, quietOnNone bool) bool {
	// An empty branch means no branch was found or created, so no PR can exist.
	if branch == "" {
		if !quietOnNone {
			fmt.Fprintln(w, "PR: none")
		}
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
	if !quietOnNone {
		fmt.Fprintln(w, "PR: none")
	}
	return false
}

// parseIssueURL checks whether arg is a full GitHub or GitLab issue URL.
// If so it returns the owner, repo name, issue number string, and true.
// Otherwise it returns the original arg as the issue and false (bare number path).
func parseIssueURL(arg string) (owner, repo, issueNum string, ok bool) {
	var prov string
	owner, repo, issueNum, prov, ok = vcs.ParseIssueURL(arg)
	_ = prov
	return
}

// locateOrCloneRepo returns the local git repo root for the given owner/repoName,
// using p for cloning when the repo is not found locally.
// It searches in order:
//  1. The repo that contains the current working directory.
//  2. A sibling directory named <repoName> (i.e. "../<repoName>").
//  3. Clones the repo into "../<repoName>" via the provider CLI.
func locateOrCloneRepo(owner, repoName string, p vcs.Provider, out io.Writer) (string, error) {
	// 1. Current working directory.
	if root, err := git.RepoRoot(); err == nil && vcs.MatchesOrigin(root, owner, repoName, p) {
		return root, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}

	// 2. Sibling directory.
	sibling := filepath.Join(filepath.Dir(cwd), repoName)
	if info, statErr := os.Stat(sibling); statErr == nil && info.IsDir() {
		if vcs.MatchesOrigin(sibling, owner, repoName, p) {
			return sibling, nil
		}
		return "", fmt.Errorf("directory %s exists but does not match %s/%s", sibling, owner, repoName)
	}

	// 3. Clone via provider CLI.
	target := filepath.Join(filepath.Dir(cwd), repoName)
	fmt.Fprintf(out, "Cloning %s/%s into %s ...\n", owner, repoName, target)
	if err := p.Clone(owner+"/"+repoName, target, out); err != nil {
		return "", err
	}
	return target, nil
}

// repoRootForIssue resolves the local git repo root, bare issue number, the
// issue argument to pass to the VCS CLI, and the detected provider.
//
// When arg is a bare issue number the repo is inferred from the current working
// directory. When arg is a full issue URL (GitHub or GitLab) the target
// repository is located or cloned automatically.
func repoRootForIssue(arg string, out io.Writer) (repoRoot, issueNum, issueArg string, p vcs.Provider, err error) {
	owner, repoName, issueNum, providerName, isURL := vcs.ParseIssueURL(arg)
	if !isURL {
		root, rootErr := git.RepoRoot()
		if rootErr != nil {
			return "", "", "", nil, fmt.Errorf("cannot determine repo root: %w", rootErr)
		}
		provider, detectErr := vcs.Detect(root)
		if detectErr != nil {
			return "", "", "", nil, detectErr
		}
		return root, arg, arg, provider, nil
	}
	urlProvider, provErr := vcs.ProviderForName(providerName)
	if provErr != nil {
		return "", "", "", nil, provErr
	}
	root, cloneErr := locateOrCloneRepo(owner, repoName, urlProvider, out)
	if cloneErr != nil {
		return "", "", "", nil, cloneErr
	}
	// Re-detect after clone so any config-file override in the cloned repo is honoured.
	provider, detectErr := vcs.Detect(root)
	if detectErr != nil {
		provider = urlProvider
	}
	// Pass the original URL as issueArg so the VCS CLI can resolve it without
	// requiring a matching git remote.
	return root, issueNum, arg, provider, nil
}

// worktreeExistsError returns a descriptive, actionable error for the case
// where the target worktree directory already exists. It reads the .agent
// metadata to distinguish between a still-running agent, a finished agent,
// and a bare worktree with no .agent file.
func worktreeExistsError(wtPath, issueNum string) error {
	af, readErr := state.Read(wtPath)
	id := issueNum
	if readErr == nil && af.IssueArg != "" {
		id = af.IssueArg
	}
	if readErr == nil && af.AgentPID != "" && process.IsAlive(af.AgentPID) {
		return fmt.Errorf("Worktree already exists for issue %s — agent is still running.\nWorktree: %s\n\n  agentctl attach %s   to follow its output\n  agentctl discard %s   to delete the worktree and start over", issueNum, wtPath, id, id)
	}
	if readErr == nil && af.AgentPID != "" {
		return fmt.Errorf("Worktree already exists for issue %s — agent has finished.\nWorktree: %s\n\n  agentctl cleanup %s   if the PR is merged\n  agentctl discard %s   to delete the worktree and start over", issueNum, wtPath, id, id)
	}
	return fmt.Errorf("Worktree already exists for issue %s.\nWorktree: %s\n\n  agentctl discard %s   to delete the worktree and start over", issueNum, wtPath, id)
}

// taskWorktreeExistsError is like worktreeExistsError but uses "task"/"branch"
// wording instead of "issue" for task-mode worktrees.
func taskWorktreeExistsError(wtPath, branch string) error {
	af, readErr := state.Read(wtPath)
	if readErr == nil && af.AgentPID != "" && process.IsAlive(af.AgentPID) {
		return fmt.Errorf("Worktree already exists for task branch %s — agent is still running.\nWorktree: %s\n\n  agentctl attach %s   to follow its output\n  agentctl discard %s   to delete the worktree and start over", branch, wtPath, branch, branch)
	}
	if readErr == nil && af.AgentPID != "" {
		return fmt.Errorf("Worktree already exists for task branch %s — agent has finished.\nWorktree: %s\n\n  agentctl cleanup %s   if the PR is merged\n  agentctl discard %s   to delete the worktree and start over", branch, wtPath, branch, branch)
	}
	return fmt.Errorf("Worktree already exists for task branch %s.\nWorktree: %s\n\n  agentctl discard %s   to delete the worktree and start over", branch, wtPath, branch)
}

// issueDisplayFor reads the issue-arg stored in the .agent state file and
// returns it when non-empty. Falls back to fallback (the bare issue number)
// when the state cannot be read or the field is absent, so callers always get
// a usable string regardless of worktree age.
func issueDisplayFor(wtPath, fallback string) string {
	if af, err := state.Read(wtPath); err == nil && af.IssueArg != "" {
		return af.IssueArg
	}
	return fallback
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
				} else if removeErr := removeAllWritable(wtPath); removeErr != nil {
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

// slugFromTask derives a task branch name from the first six words of the
// task description using the same slugging rules as GitHub issue titles.
func slugFromTask(description string) string {
	words := strings.Fields(description)
	if len(words) > 6 {
		words = words[:6]
	}
	slug := titleToSlug(strings.Join(words, " "))
	if slug == "" {
		slug = "task"
	}
	return "task/" + slug
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
// On success the URL is printed to out and the PID/port are returned to the
// caller for storage in the .agent state file. .agentctl.yml is not modified.
func startDevServer(dir string, out io.Writer) (devPID, portStr string, err error) {
	cfg, err := config.ReadMerged(dir)
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
	detachProcess(devCmd)
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
		if _, _, num, ok := parseIssueURL(args[0]); ok {
			return num, nil
		}
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
		if branch != "" && branch != "HEAD" {
			return branch, nil
		}
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

// errAmbiguousWorktree is returned by resolveWorktree when multiple worktrees
// exist for an issue and no --agent flag was supplied to disambiguate.
var errAmbiguousWorktree = errors.New("ambiguous worktree")

// errWorktreeNotFound is returned by resolveWorktree when no worktree matches
// the given issue (and optional agent suffix), as distinct from a real git error.
var errWorktreeNotFound = errors.New("worktree not found")

// agentSuffixRe restricts agent names used as worktree/branch suffixes to safe
// characters. Slashes, spaces, and other special chars would create nested
// directories or invalid git branch names.
var agentSuffixRe = regexp.MustCompile(`^[a-z0-9_-]+$`)

// validateAgentSuffix returns an error when suffix contains characters that
// would create an invalid git branch name or unexpected filesystem path.
// An empty suffix is always valid (single-agent mode).
func validateAgentSuffix(suffix string) error {
	if suffix == "" {
		return nil
	}
	if !agentSuffixRe.MatchString(suffix) {
		return fmt.Errorf("invalid agent name %q: must match [a-z0-9_-]+", suffix)
	}
	return nil
}

// worktreeNames computes the branch name and filesystem path for a worktree.
// When agentSuffix is non-empty (multi-agent mode) it is appended to both so
// each agent gets an isolated workspace: <issue>-<slug>-<agent> / <repo>-<issue>-<slug>-<agent>.
func worktreeNames(repoName, issueNum, slug, agentSuffix, parentDir string) (branch, wtPath string) {
	branch = issueNum + "-" + slug
	wtPath = filepath.Join(parentDir, repoName+"-"+issueNum+"-"+slug)
	if agentSuffix != "" {
		branch += "-" + agentSuffix
		wtPath += "-" + agentSuffix
	}
	return
}

// resolveWorktree finds the linked worktree for an issue, handling the
// single-agent (no suffix) and multi-agent (suffix) cases.
//
//   - agentSuffix non-empty: find the worktree whose path ends with "-<agentSuffix>".
//   - agentSuffix empty, exactly one match: return it (unchanged single-agent behaviour).
//   - agentSuffix empty, multiple matches: return errAmbiguousWorktree so the
//     caller can ask the user to supply --agent.
//
// For non-numeric refs (e.g. task-mode branch names like "task/refactor-auth"),
// falls back to an exact branch lookup via FindWorktreeByBranch.
//
// Returns errWorktreeNotFound (wrapped) when no matching worktree exists.
func resolveWorktree(repoRoot, issue, agentSuffix string) (git.Worktree, error) {
	wts, err := git.FindWorktreesByIssue(repoRoot, issue)
	if err != nil {
		return git.Worktree{}, err
	}

	// For non-numeric refs (task-mode branch names), try exact branch lookup
	// when issue-based path matching finds nothing.
	if len(wts) == 0 && !isNumericIssue(issue) {
		wt, found, branchErr := git.FindWorktreeByBranch(repoRoot, issue)
		if branchErr != nil {
			return git.Worktree{}, branchErr
		}
		if found {
			return wt, nil
		}
		return git.Worktree{}, fmt.Errorf("no worktree found for issue %s — has it been started?: %w", issue, errWorktreeNotFound)
	}

	if agentSuffix != "" {
		suffix := "-" + agentSuffix
		for _, wt := range wts {
			if strings.HasSuffix(wt.Path, suffix) {
				return wt, nil
			}
		}
		return git.Worktree{}, fmt.Errorf("no worktree found for issue %s (agent: %s) — has it been started?: %w", issue, agentSuffix, errWorktreeNotFound)
	}

	switch len(wts) {
	case 0:
		return git.Worktree{}, fmt.Errorf("no worktree found for issue %s — has it been started?: %w", issue, errWorktreeNotFound)
	case 1:
		return wts[0], nil
	default:
		var agents []string
		for _, wt := range wts {
			af, _ := state.Read(wt.Path)
			if af.Agent != "" {
				agents = append(agents, af.Agent)
			} else {
				agents = append(agents, filepath.Base(wt.Path))
			}
		}
		return git.Worktree{}, fmt.Errorf("multiple worktrees found for issue %s; specify --agent (%s): %w",
			issue, strings.Join(agents, ", "), errAmbiguousWorktree)
	}
}

// findWorktreePath resolves the linked worktree path for the given issue.
// agentSuffix disambiguates when multiple worktrees exist for the same issue
// (created with --agent claude,codex). Pass "" for the original single-agent behaviour.
func findWorktreePath(issue, agentSuffix string) (string, error) {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return "", fmt.Errorf("cannot determine repo root: %w", err)
	}
	wt, err := resolveWorktree(repoRoot, issue, agentSuffix)
	if err != nil {
		return "", err
	}
	return wt.Path, nil
}

// launchAgent starts the coding agent in the background via the named adapter,
// then either returns immediately (headless) or streams agent.log to stdout
// until the agent exits (non-headless). quiet suppresses log lines, showing
// only the spinner/heartbeat.
func launchAgent(adapterName, wtPath, issue, port, sessionID, kickoff, sddName string, headless, quiet, sendNotify bool, out io.Writer) error {
	// --quiet uses the detached subprocess log router (same as headless) so
	// the converter survives if the user Ctrl+C's, but the parent still waits
	// for the agent to finish (foreground behaviour is preserved).
	detachedRouter := headless || quiet

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
	if !detachedRouter {
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
			// A detached __stream-log subprocess converts the pipe to human-readable
			// text in agent.log and survives the parent exiting.
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
	if detachedRouter {
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

		id := issueDisplayFor(wtPath, issue)
		fmt.Fprintf(out, "Agent PID %d — log: %s\n", pid, logPath)
		fmt.Fprintf(out, "agentctl logs %s      # follow log\n", id)
		fmt.Fprintf(out, "agentctl attach %s    # stream live and wait\n", id)
		fmt.Fprintf(out, "agentctl discard %s   # abandon\n", id)
		if sddName != "" {
			fmt.Fprintf(out, "agentctl resume %s [feedback]   # approve spec or send revisions\n", id)
		}
		if sendNotify {
			maybeFireTestNotification(issue, out)
			go func() {
				<-exitCh
				sendCompletionNotification(issue, wtPath, sddName, agentExitErr)
			}()
		}
		if err := startDetachedDiagnosticsFinaliser(wtPath, pid); err != nil {
			return err
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
				id2 := issueDisplayFor(wtPath, issue)
				fmt.Fprintf(out, "agent exited with error — check the log: agentctl logs %s\n", id2)
				return nil
			}
			branch, branchErr := git.CurrentBranch(wtPath)
			if branchErr != nil {
				branch = ""
			}
			hasPR := reportPRStatus(out, wtPath, branch, issue, sddName != "")
			af3, _ := state.Read(wtPath)
			if af3.DevPort != "" {
				fmt.Fprintf(out, "Dev server: http://localhost:%s\n", af3.DevPort)
			}
			id3 := af3.IssueArg
			if id3 == "" {
				id3 = issue
			}
			if sddName != "" && !hasPR {
				if specPath := findSpecPath(wtPath, issue); specPath != "" {
					fmt.Fprintf(out, "Spec: %s\n", specPath)
				}
				fmt.Fprintf(out, resumeHintFmt, id3)
			}
			if hasPR {
				fmt.Fprintf(out, "agentctl cleanup %s   # delete worktree + branch after PR is merged\n", id3)
			}
			return nil
		case <-sigCh:
			signal.Stop(sigCh)
			close(logDone)
			wg.Wait()
			id4 := issueDisplayFor(wtPath, issue)
			fmt.Fprintf(out, "agent still running in background\n")
			fmt.Fprintf(out, "  agentctl logs %s     # follow log\n", id4)
			fmt.Fprintf(out, "  agentctl attach %s   # stream live output\n", id4)
			fmt.Fprintf(out, "  agentctl discard %s  # permanently delete worktree and branches\n", id4)
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
	specPath := findSpecPath(wtPath, issue)
	af, _ := state.Read(wtPath)
	var message string
	switch {
	case exitErr != nil:
		message = fmt.Sprintf("Agent failed — issue #%s%s: check agentctl logs %s", issue, branchPart, issue)
	case sddName != "" && af.SDDStage < 2 && specPath != "":
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

func startDetachedDiagnosticsFinaliser(wtPath string, pid int) error {
	cmd := exec.Command(os.Args[0], "__finalise-diagnostics", wtPath, strconv.Itoa(pid))
	cmd.Dir = wtPath
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start diagnostics finaliser: %w", err)
	}
	return cmd.Process.Release()
}

// runFinaliseDiagnostics waits for pid to exit, then finalises diagnostics for
// the worktree.
func runFinaliseDiagnostics(wtPath, pid string) error {
	const (
		processCheckInterval = 200 * time.Millisecond
		logFlushWait         = 200 * time.Millisecond
	)
	for process.IsAlive(pid) {
		time.Sleep(processCheckInterval)
	}
	// Give detached log converters a moment to flush final output.
	time.Sleep(logFlushWait)

	af, _ := state.Read(wtPath)
	repoRoot := repoRootFromWorktree(wtPath)
	if repoRoot == "" {
		return nil
	}

	exitReason := "failed"
	prURL := af.PRURL
	if prURL == "" {
		branch, _ := git.CurrentBranch(wtPath)
		if branch != "" {
			if p, pErr := resolveProvider(repoRoot); pErr == nil {
				if _, _, foundURL, pErr := p.PRForBranch(repoRoot, branch); pErr == nil && foundURL != "" {
					prURL = foundURL
				}
			}
		}
	}
	if prURL != "" {
		exitReason = "pr_opened"
	}
	af.PRURL = prURL
	finaliseDiagnostics(repoRoot, wtPath, af, exitReason)
	return nil
}

// NewFinaliseDiagnosticsCmd returns a hidden command used by headless start and
// resume paths to finalise diagnostics after the parent process exits.
func NewFinaliseDiagnosticsCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "__finalise-diagnostics <wtDir> <pid>",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return runFinaliseDiagnostics(args[0], args[1])
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

	// Write a .gitignore that ignores all contents of .agent-home. This
	// prevents build tools that respect .gitignore (e.g. Turbopack) from
	// following symlinks inside here that point outside the project root.
	// Use Lstat + O_EXCL so a malicious repo cannot plant a symlink at
	// .agent-home/.gitignore and redirect our write to a path outside the
	// worktree.
	gitignorePath := filepath.Join(agentHome, ".gitignore")
	if fi, err := os.Lstat(gitignorePath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("agentEnv: .agent-home/.gitignore is a symlink; refusing to write")
		}
	} else if os.IsNotExist(err) {
		f, createErr := os.OpenFile(gitignorePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if createErr == nil {
			_, _ = f.Write([]byte("*\n"))
			_ = f.Close()
		} else if !errors.Is(createErr, os.ErrExist) {
			fmt.Fprintf(os.Stderr, "agentctl: warning: failed to create %s: %v\n", gitignorePath, createErr)
		}
	} else {
		fmt.Fprintf(os.Stderr, "agentctl: warning: failed to stat %s: %v\n", gitignorePath, err)
	}

	// Also add .agent-home to the worktree-local git exclude so it doesn't
	// appear in git status. This file lives in the worktree's own git metadata
	// dir and is never committed.
	if out, err := exec.Command("git", "-C", wtPath, "rev-parse", "--git-dir").Output(); err == nil {
		gitDir := strings.TrimSpace(string(out))
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(wtPath, gitDir)
		}
		excludePath := filepath.Join(gitDir, "info", "exclude")
		if content, err := os.ReadFile(excludePath); err == nil {
			hasAgentHomeExclude := false
			for _, line := range strings.Split(string(content), "\n") {
				if strings.TrimSpace(line) == ".agent-home" {
					hasAgentHomeExclude = true
					break
				}
			}
			if !hasAgentHomeExclude {
				updatedContent := content
				if !bytes.HasSuffix(updatedContent, []byte("\n")) {
					updatedContent = append(updatedContent, '\n')
				}
				updatedContent = append(updatedContent, []byte(".agent-home\n")...)
				_ = os.WriteFile(excludePath, updatedContent, 0o644)
			}
		}
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

		// Expose ~/.config/gh and ~/.config/glab-cli (the CLI credential stores)
		// rather than the entire ~/.config tree, to limit the host config surface
		// accessible from the agent's isolated HOME.
		agentConfigDir := filepath.Join(agentHome, ".config")
		for _, cfgDir := range []string{"gh", "glab-cli"} {
			cfgSrc := filepath.Join(realHome, ".config", cfgDir)
			if _, srcErr := os.Lstat(cfgSrc); srcErr != nil {
				if !os.IsNotExist(srcErr) {
					fmt.Fprintf(os.Stderr, "agentctl: warning: stat %s: %v\n", cfgSrc, srcErr)
				}
				continue
			}
			if mkdirErr := os.MkdirAll(agentConfigDir, 0o755); mkdirErr != nil {
				fmt.Fprintf(os.Stderr, "agentctl: warning: mkdir %s: %v\n", agentConfigDir, mkdirErr)
				continue
			}
			cfgDst := filepath.Join(agentConfigDir, cfgDir)
			if _, statErr := os.Lstat(cfgDst); os.IsNotExist(statErr) {
				if symlinkErr := os.Symlink(cfgSrc, cfgDst); symlinkErr != nil {
					fmt.Fprintf(os.Stderr, "agentctl: warning: symlink .config/%s: %v (%s credentials may not work)\n", cfgDir, symlinkErr, cfgDir)
				}
			}
		}
	}

	env := os.Environ()

	// GITHUB_TOKEN is guaranteed to be set by ensureGHToken(), which all
	// agent-launch entrypoints (startOne, agentResume) call before reaching here.
	// os.Environ() picks it up automatically; nothing extra needed here.

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
	// Verify VCS credentials are available before any provider calls.
	if p, err := resolveProvider(wtPath); err == nil {
		if authErr := p.AuthCheck(); authErr != nil {
			return authErr
		}
	}

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
		id := issueDisplayFor(wtPath, issue)
		fmt.Printf("Released pause for issue %s; Stage 2 running in background.\n", issue)
		fmt.Printf("agentctl logs %s      # follow log\n", id)
		fmt.Printf("agentctl attach %s    # stream live and wait\n", id)
		fmt.Printf("agentctl discard %s   # abandon\n", id)
		if sendNotify {
			maybeFireTestNotification(issue, os.Stdout)
			go func() {
				<-exitCh
				sendCompletionNotification(issue, wtPath, "", resumeExitErr)
			}()
		}
		if err := startDetachedDiagnosticsFinaliser(wtPath, pid); err != nil {
			return err
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
			af2, _ := state.Read(wtPath)
			resumeSDD := af2.SDD
			hasPR := reportPRStatus(os.Stdout, wtPath, branch, issue, resumeSDD != "")
			id := af2.IssueArg
			if id == "" {
				id = issue
			}
			if resumeSDD != "" && !hasPR {
				if specPath := findSpecPath(wtPath, issue); specPath != "" {
					fmt.Fprintf(os.Stdout, "Spec: %s\n", specPath)
				}
				fmt.Fprintf(os.Stdout, resumeHintFmt, id)
			}
			if hasPR {
				fmt.Fprintf(os.Stdout, "agentctl cleanup %s   # delete worktree + branch after PR is merged\n", id)
			}
			return nil
		case <-sigCh:
			signal.Stop(sigCh)
			close(logDone)
			wg.Wait()
			id2 := issueDisplayFor(wtPath, issue)
			fmt.Fprintf(os.Stdout, "agent still running in background\n")
			fmt.Fprintf(os.Stdout, "  agentctl logs %s     # follow log\n", id2)
			fmt.Fprintf(os.Stdout, "  agentctl attach %s   # stream live output\n", id2)
			fmt.Fprintf(os.Stdout, "  agentctl discard %s  # permanently delete worktree and branches\n", id2)
			return nil
		}
	}
}

// ─── report ───────────────────────────────────────────────────────────────────

// NewReportCmd creates the `report` subcommand.
func NewReportCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "report",
		Short: "Aggregate view of all agent runs in the current repo",
		Long: `Print a summary of all agent runs recorded in .agentctl/runs/.

Default output shows period aggregates (last 7 and 30 days) and the
slowest individual runs. Use --json for machine-readable output suitable
for piping into dashboards or cost-accounting scripts.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReport(asJSON, os.Stdout)
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "Emit all run records as a JSON array")
	return c
}

func runReport(asJSON bool, out io.Writer) error {
	repoRoot, err := git.RepoRoot()
	if err != nil {
		return fmt.Errorf("cannot determine repo root: %w", err)
	}
	records, err := diagnostics.List(repoRoot)
	if err != nil {
		return fmt.Errorf("reading run records: %w", err)
	}

	if asJSON {
		if records == nil {
			records = []*diagnostics.RunRecord{}
		}
		data, err := json.MarshalIndent(records, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(data))
		return nil
	}

	now := time.Now()
	periods := []struct {
		label string
		since time.Duration
	}{
		{"Last 7 days", 7 * 24 * time.Hour},
		{"Last 30 days", 30 * 24 * time.Hour},
	}

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PERIOD\tRUNS\tSUCCESS\tFAILED\tAVG TIME\tTOTAL TOKENS")
	for _, p := range periods {
		cutoff := now.Add(-p.since)
		var total, success, failed int
		var totalSec, totalTokens float64
		for _, r := range records {
			if r.StartedAt.Before(cutoff) {
				continue
			}
			total++
			switch r.ExitReason {
			case "pr_opened":
				success++
			case "failed", "discarded":
				failed++
			}
			totalSec += r.ElapsedSeconds
			totalTokens += float64(r.TokensUsed)
		}
		avgTime := "-"
		if total > 0 {
			avg := time.Duration((totalSec / float64(total)) * float64(time.Second))
			avgTime = formatDuration(avg)
		}
		totalTokStr := "-"
		if totalTokens > 0 {
			totalTokStr = formatTokens(totalTokens)
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\t%s\n",
			p.label, total, success, failed, avgTime, totalTokStr)
	}
	_ = tw.Flush()

	// Slowest completed runs.
	type slowRun struct {
		issue   string
		branch  string
		elapsed float64
		outcome string
	}
	var completed []slowRun
	for _, r := range records {
		if r.ExitReason == "in_progress" || r.ElapsedSeconds == 0 {
			continue
		}
		completed = append(completed, slowRun{
			issue:   r.Issue,
			branch:  r.Branch,
			elapsed: r.ElapsedSeconds,
			outcome: r.ExitReason,
		})
	}
	// Sort descending by elapsed.
	for i := 0; i < len(completed)-1; i++ {
		for j := i + 1; j < len(completed); j++ {
			if completed[j].elapsed > completed[i].elapsed {
				completed[i], completed[j] = completed[j], completed[i]
			}
		}
	}
	if len(completed) > 10 {
		completed = completed[:10]
	}
	if len(completed) > 0 {
		fmt.Fprintln(out, "\nSLOWEST RUNS")
		tw2 := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw2, "ISSUE\tBRANCH\tELAPSED\tOUTCOME")
		for _, r := range completed {
			fmt.Fprintf(tw2, "%s\t%s\t%s\t%s\n",
				r.issue, r.branch,
				formatDuration(time.Duration(r.elapsed)*time.Second),
				r.outcome)
		}
		_ = tw2.Flush()
	}
	return nil
}

// formatDuration renders a duration as "Xh Ym Zs" (or just "Ym Zs" / "Zs").
func formatDuration(d time.Duration) string {
	d = d.Truncate(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %02dm %02ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// formatTokens renders a token count as "1.2M", "87.4K", or the raw number.
func formatTokens(t float64) string {
	switch {
	case t >= 1_000_000:
		return fmt.Sprintf("%.1fM", t/1_000_000)
	case t >= 1_000:
		return fmt.Sprintf("%.1fK", t/1_000)
	default:
		return fmt.Sprintf("%.0f", t)
	}
}

// ─── diagnostics helpers ──────────────────────────────────────────────────────

// isDiagnosticsEnabled reports whether per-run diagnostics are enabled for repoRoot.
// Diagnostics are on by default; set diagnostics.enabled: false in .agentctl.yml to opt out.
func isDiagnosticsEnabled(repoRoot string) bool {
	cfg, err := config.ReadMerged(repoRoot)
	if err != nil || cfg.Diagnostics.Enabled == nil {
		return true
	}
	return *cfg.Diagnostics.Enabled
}

// repoRootFromWorktree returns the primary (main) worktree path by running
// `git worktree list --porcelain` from wtPath. Returns "" on failure.
func repoRootFromWorktree(wtPath string) string {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = wtPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	var first, currWT, currGitDir string
	lines := append(strings.Split(string(out), "\n"), "")
	for _, line := range lines {
		if line == "" {
			if currWT != "" {
				if first == "" {
					first = currWT
				}
				if sameGitDir(currWT, currGitDir, filepath.Join(currWT, ".git")) {
					return currWT
				}
			}
			currWT, currGitDir = "", ""
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			currWT = strings.TrimPrefix(line, "worktree ")
			continue
		}
		if strings.HasPrefix(line, "gitdir ") {
			currGitDir = strings.TrimPrefix(line, "gitdir ")
		}
	}
	return first
}

func sameGitDir(worktree, a, b string) bool {
	normalize := func(p string) string {
		if p == "" {
			return ""
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(worktree, p)
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		return filepath.Clean(p)
	}
	return normalize(a) == normalize(b)
}

// countFilesChanged counts unique files modified on the current branch relative
// to the first reachable base branch among main, master, origin/main, origin/master.
// Falls back to counting uncommitted (staged+unstaged) changes.
func countFilesChanged(wtPath string) int {
	for _, base := range []string{"main", "master", "origin/main", "origin/master"} {
		cmd := exec.Command("git", "diff", "--name-only", base+"...HEAD")
		cmd.Dir = wtPath
		out, err := cmd.Output()
		if err == nil {
			return countNonEmptyLines(string(out))
		}
	}
	seen := map[string]struct{}{}
	for _, args := range [][]string{
		{"diff", "--name-only"},
		{"diff", "--cached", "--name-only", "HEAD"},
		{"ls-files", "--others", "--exclude-standard"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = wtPath
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" {
				seen[line] = struct{}{}
			}
		}
	}
	return len(seen)
}

// countNonEmptyLines returns the number of non-empty lines in s.
func countNonEmptyLines(s string) int {
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line != "" {
			n++
		}
	}
	return n
}

// finaliseDiagnostics updates the run record for wtPath with stop time,
// exit reason, PR URL, and files changed, then appends to the global history.
func finaliseDiagnostics(repoRoot, wtPath string, af state.AgentFile, exitReason string) {
	runFile := af.Extra["run-file"]
	if runFile == "" || !isDiagnosticsEnabled(repoRoot) {
		return
	}
	stoppedAt := time.Now()
	prURL := af.PRURL
	filesChanged := countFilesChanged(wtPath)
	_ = diagnostics.Update(repoRoot, runFile, func(r *diagnostics.RunRecord) {
		r.StoppedAt = &stoppedAt
		r.ElapsedSeconds = stoppedAt.Sub(r.StartedAt).Seconds()
		r.ExitReason = exitReason
		if prURL != "" {
			r.PRURL = prURL
		}
		r.FilesChanged = filesChanged
	})
	// Append a snapshot to the global cross-repo history.
	if rec, err := diagnostics.Read(repoRoot, runFile); err == nil {
		_ = diagnostics.AppendGlobal(&rec)
	}
}
