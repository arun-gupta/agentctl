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
	AgentPID  string // PID of the background agent process (headless only)
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
		case "agent-pid":
			af.AgentPID = v
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
	case "agent-pid":
		return af.AgentPID, nil
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
	if af.AgentPID != "" {
		lines = append(lines, "agent-pid="+af.AgentPID)
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
