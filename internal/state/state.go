// Package state handles reading and writing the .agent key=value state file
// that agentctl creates in each worktree root.
package state

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const FileName = ".agent"

// AgentFile holds the key-value pairs stored in a worktree's .agent file.
type AgentFile struct {
	Agent     string // adapter name, e.g. "claude"
	SessionID string // UUID assigned at spawn time
	DevPID    string // PID of the dev server process
	DevPort   string // port allocated for the dev server; empty when no dev server
	AgentPID  string // PID of the background agent process (headless only)
	SDD       string // SDD methodology name, e.g. "plain", "speckit"; empty if none
	IssueArg  string // canonical GitHub issue URL for display hints (e.g. https://github.com/owner/repo/issues/42); empty when not set
	PRNumber  string // PR number, e.g. "42"; empty until first agentctl status after PR opens
	PRURL     string // full PR URL, e.g. "https://github.com/owner/repo/pull/42"; empty until first agentctl status after PR opens
	// SDDSet is true when the sdd= key was explicitly present in the .agent file,
	// even if its value is empty. Use this to distinguish new worktrees created
	// without --sdd (SDDSet=true, SDD="") from legacy worktrees written before the
	// sdd key was introduced (SDDSet=false).
	SDDSet bool
	// Extra holds any additional key=value pairs not explicitly modelled above.
	Extra map[string]string
}

// Read parses the .agent file at <worktreePath>/.agent.
// Missing keys are left as empty strings; a missing file is returned as
// a zero-value AgentFile with no error (so callers can treat it as "not set").
func Read(worktreePath string) (AgentFile, error) {
	path := filepath.Join(worktreePath, FileName)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return AgentFile{}, nil
	}
	if err != nil {
		return AgentFile{}, err
	}
	defer f.Close()

	af := AgentFile{Extra: make(map[string]string)}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		k, v := line[:idx], line[idx+1:]
		switch k {
		case "agent":
			af.Agent = v
		case "session-id":
			af.SessionID = v
		case "dev-pid":
			af.DevPID = v
		case "dev-port":
			af.DevPort = v
		case "agent-pid":
			af.AgentPID = v
		case "sdd":
			af.SDD = v
			af.SDDSet = true
		case "issue-arg":
			af.IssueArg = v
		case "pr":
			af.PRNumber = v
		case "pr-url":
			af.PRURL = v
		default:
			af.Extra[k] = v
		}
	}
	return af, sc.Err()
}

// GetKey returns the value for a specific key from the .agent file, or ""
// if the key is not present or the file does not exist.
func GetKey(worktreePath, key string) (string, error) {
	af, err := Read(worktreePath)
	if err != nil {
		return "", err
	}
	switch key {
	case "agent":
		return af.Agent, nil
	case "session-id":
		return af.SessionID, nil
	case "dev-pid":
		return af.DevPID, nil
	case "dev-port":
		return af.DevPort, nil
	case "agent-pid":
		return af.AgentPID, nil
	case "sdd":
		return af.SDD, nil
	case "issue-arg":
		return af.IssueArg, nil
	case "pr":
		return af.PRNumber, nil
	case "pr-url":
		return af.PRURL, nil
	default:
		return af.Extra[key], nil
	}
}

// Write creates (or truncates) the .agent file with the provided fields.
// This is called by the spawn command after the worktree is provisioned.
func Write(worktreePath string, af AgentFile) error {
	path := filepath.Join(worktreePath, FileName)
	lines := []string{
		"agent=" + af.Agent,
		"session-id=" + af.SessionID,
		"dev-pid=" + af.DevPID,
	}
	if af.DevPort != "" {
		lines = append(lines, "dev-port="+af.DevPort)
	}
	if af.AgentPID != "" {
		lines = append(lines, "agent-pid="+af.AgentPID)
	}
	// Always write sdd= so readers can distinguish new worktrees created without
	// --sdd (sdd= present but empty) from legacy worktrees that predate this key.
	lines = append(lines, "sdd="+af.SDD)
	if af.IssueArg != "" {
		lines = append(lines, "issue-arg="+af.IssueArg)
	}
	if af.PRNumber != "" {
		lines = append(lines, "pr="+af.PRNumber)
	}
	if af.PRURL != "" {
		lines = append(lines, "pr-url="+af.PRURL)
	}
	for k, v := range af.Extra {
		lines = append(lines, k+"="+v)
	}
	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0o600)
}

// AppendKey appends a single key=value line to the .agent file.
// This matches the bash `echo "key=val" >> .agent` pattern used by adapters
// to append the agent-pid after the core fields have already been written.
func AppendKey(worktreePath, key, value string) error {
	path := filepath.Join(worktreePath, FileName)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s=%s\n", key, value)
	return err
}
