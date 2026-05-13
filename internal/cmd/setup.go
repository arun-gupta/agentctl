package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/arun-gupta/agentctl/internal/adapters"
	"github.com/arun-gupta/agentctl/internal/config"
	"github.com/arun-gupta/agentctl/internal/sdd"
)

// writeGlobalFn allows tests to stub global config writes.
var writeGlobalFn = config.WriteGlobal

// NewSetupCmd creates the `setup` subcommand — an interactive wizard that
// guides new developers through configuring agentctl for first use.
func NewSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Interactive setup wizard for new developers",
		Long: `Guide new developers through configuring agentctl for first use.

Checks prerequisites (git, GitHub CLI), shows available coding agents,
and prompts for preferences (default agent, notifications, SDD methodology)
that are saved to the global config file.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

func runSetup(in io.Reader, out io.Writer) error {
	ascii := os.Getenv("AGENTCTL_ASCII") == "1"
	sym := newSymbols(ascii)

	fmt.Fprintln(out, "Welcome to agentctl setup! Let's configure your environment.")
	fmt.Fprintln(out)

	// Step 1: check prerequisites.
	fmt.Fprintln(out, "Checking prerequisites...")
	var missingPrereqs []string

	if gitVer, err := runToolCmdFn("git", "--version"); err != nil {
		fmt.Fprintf(out, "%s Git not found — install from https://git-scm.com\n", sym.cross)
		missingPrereqs = append(missingPrereqs, "git")
	} else {
		ver := strings.TrimPrefix(strings.TrimSpace(gitVer), "git version ")
		fmt.Fprintf(out, "%s Git %s installed\n", sym.check, ver)
	}

	if _, err := lookPathFn("gh"); err != nil {
		fmt.Fprintf(out, "%s GitHub CLI not found — install with: brew install gh\n", sym.cross)
		missingPrereqs = append(missingPrereqs, "gh")
	} else {
		if ghVer, err := runToolCmdFn("gh", "--version"); err == nil {
			ver := parseGHVersion(ghVer)
			if user := ghAuthUserFn(); user != "" {
				fmt.Fprintf(out, "%s GitHub CLI %s (authenticated as %s)\n", sym.check, ver, user)
			} else {
				fmt.Fprintf(out, "%s GitHub CLI %s (not authenticated — run: gh auth login)\n", sym.check, ver)
			}
		} else {
			fmt.Fprintf(out, "%s GitHub CLI found\n", sym.check)
		}
	}
	fmt.Fprintln(out)

	// Load existing global config so we preserve unrelated fields and can
	// display the current default in each prompt.
	existingCfg, err := config.ReadGlobal()
	if err != nil {
		if os.IsNotExist(err) {
			existingCfg = &config.GlobalConfig{}
		} else {
			return fmt.Errorf("failed to read global config: %w", err)
		}
	}
	if existingCfg == nil {
		existingCfg = &config.GlobalConfig{}
	}
	currentDefault := existingCfg.DefaultAgent
	if currentDefault == "" {
		currentDefault = "claude"
	}

	// Step 2: show coding agents.
	allAdapters := adapters.List()

	hasCurrentDefault := false
	hasClaude := false
	defaultAgentIdx := 0
	for i, name := range allAdapters {
		if name == currentDefault {
			hasCurrentDefault = true
			defaultAgentIdx = i
		}
		if name == "claude" {
			hasClaude = true
		}
	}
	if !hasCurrentDefault {
		switch {
		case hasClaude:
			currentDefault = "claude"
			for i, name := range allAdapters {
				if name == currentDefault {
					defaultAgentIdx = i
					break
				}
			}
		case len(allAdapters) > 0:
			currentDefault = allAdapters[0]
			defaultAgentIdx = 0
		default:
			currentDefault = ""
			defaultAgentIdx = 0
		}
	}

	fmt.Fprintln(out, "Coding Agents (choose one):")

	for i, name := range allAdapters {
		adapter, err := adapters.Get(name)
		if err != nil {
			fmt.Fprintf(out, "  %d. %s %-12s (adapter error)\n", i+1, sym.cross, name)
			continue
		}
		suffix := ""
		if name == currentDefault {
			suffix = " (default)"
		}
		if binErr := adapter.CheckBinary(); binErr != nil {
			install := ""
			if adapter.Install != "" {
				install = " — install with: " + adapter.Install
			}
			fmt.Fprintf(out, "  %d. %s %-12s%s%s\n", i+1, sym.cross, name, suffix, install)
		} else {
			fmt.Fprintf(out, "  %d. %s %-12s%s\n", i+1, sym.check, name, suffix)
		}
	}
	fmt.Fprintln(out)

	scanner := bufio.NewScanner(in)

	// Prompt: default agent.
	selectedAgent := currentDefault
	fmt.Fprintf(out, "Default agent [%d]: ", defaultAgentIdx+1)
	if scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			if idx, err := strconv.Atoi(line); err == nil && idx >= 1 && idx <= len(allAdapters) {
				selectedAgent = allAdapters[idx-1]
			}
		}
	}
	fmt.Fprintln(out)

	// Prompt: desktop notifications.
	fmt.Fprintln(out, "Enable desktop notifications?")
	fmt.Fprintln(out, "  1. Yes")
	fmt.Fprintln(out, "  2. No")
	defaultNotifyChoice := "1"
	notifyEnabled := true
	if existingCfg.Notify != nil && !*existingCfg.Notify {
		defaultNotifyChoice = "2"
		notifyEnabled = false
	}
	fmt.Fprintf(out, "Choice [%s]: ", defaultNotifyChoice)
	if scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch line {
		case "1":
			notifyEnabled = true
		case "2":
			notifyEnabled = false
		}
	}
	fmt.Fprintln(out)

	// Prompt: SDD methodology.
	allSDDs := sdd.List()
	sddOptions := make([]string, 0, len(allSDDs)+1)
	sddOptions = append(sddOptions, allSDDs...)
	sddOptions = append(sddOptions, config.SDDNone)

	currentSDDIdx := 0
	for i, name := range sddOptions {
		if name == existingCfg.DefaultSDD {
			currentSDDIdx = i
			break
		}
	}

	fmt.Fprintln(out, "Preferred SDD methodology?")
	for i, name := range sddOptions {
		label := name
		if name == config.SDDNone {
			label = "none (disable SDD)"
		}
		fmt.Fprintf(out, "  %d. %s\n", i+1, label)
	}
	fmt.Fprintf(out, "Choice [%d]: ", currentSDDIdx+1)

	selectedSDD := ""
	if len(sddOptions) > 0 {
		selectedSDD = sddOptions[currentSDDIdx]
	}
	if scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			if idx, err := strconv.Atoi(line); err == nil && idx >= 1 && idx <= len(sddOptions) {
				selectedSDD = sddOptions[idx-1]
			}
		}
	}
	fmt.Fprintln(out)

	// Write updated config, preserving fields not touched by setup.
	existingCfg.DefaultAgent = selectedAgent
	existingCfg.Notify = &notifyEnabled
	existingCfg.DefaultSDD = selectedSDD

	if err := writeGlobalFn(existingCfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Fprintf(out, "Configuration saved to %s\n", config.GlobalPath())
	fmt.Fprintln(out)

	// Next steps.
	fmt.Fprintln(out, "Next steps:")
	step := 1
	for _, p := range missingPrereqs {
		switch p {
		case "git":
			fmt.Fprintf(out, "  %d. Install Git: https://git-scm.com\n", step)
		case "gh":
			fmt.Fprintf(out, "  %d. Install GitHub CLI: brew install gh\n", step)
		}
		step++
	}
	fmt.Fprintf(out, "  %d. Run 'agentctl info' to verify your setup\n", step)
	step++
	fmt.Fprintf(out, "  %d. Start working on an issue: agentctl start 42\n", step)

	return nil
}
