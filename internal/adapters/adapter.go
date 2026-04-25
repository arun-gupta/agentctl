// Package adapters implements YAML-based coding-agent adapter loading with a
// three-level resolution chain: project-local → user-level → built-in.
package adapters

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed builtin/*.yml
var builtinFS embed.FS

// Adapter describes how to launch and resume a coding agent.
// The adapter name is derived from the YAML filename stem (e.g. "claude.yml" → "claude").
type Adapter struct {
	// Binary is the command to invoke. Required. A multi-token value such as
	// "opencode run" is split on whitespace; the first token is the executable.
	Binary string `yaml:"binary"`

	// Prompt is the flag used to pass the prompt text (default: "-p").
	Prompt string `yaml:"prompt"`

	// Session is the flag used to pass the session ID on launch (default: none).
	Session string `yaml:"session"`

	// ResumeID is the flag used to pass the session ID on resume
	// (default: same as Session).
	ResumeID string `yaml:"resume_id"`

	// SessionType controls session-continuity strategy.
	// "flag" (default) — session ID passed via Session/ResumeID flags.
	// "directory"       — no session flags; continuity is implicit in the worktree.
	SessionType string `yaml:"session_type"`

	// LaunchTemplate is a full launch command override. Placeholders:
	// {kickoff}, {session_id}. When set, Binary/Prompt/Session are
	// ignored for launch.
	LaunchTemplate string `yaml:"launch"`

	// ResumeCmdTemplate is a full resume command override. Placeholders:
	// {prompt}, {session_id}, {kickoff}. When set, Binary/Prompt/ResumeID are
	// ignored for resume.
	ResumeCmdTemplate string `yaml:"resume_cmd"`

	// Install is an optional hint shown when the binary is not found on PATH.
	// Example: "npm install -g @anthropic-ai/claude-code"
	Install string `yaml:"install"`

	// source is the file path this adapter was loaded from (not in YAML).
	source string
}

// LaunchCmd returns an *exec.Cmd that starts the agent in the given worktree.
// kickoff is the multi-line kickoff prompt; sessionID is the UUID assigned by
// agentctl.
func (a *Adapter) LaunchCmd(kickoff, sessionID string) *exec.Cmd {
	if a.LaunchTemplate != "" {
		return buildFromTemplate(a.LaunchTemplate, map[string]string{
			"{kickoff}":    kickoff,
			"{session_id}": sessionID,
		})
	}
	return a.buildStructuredCmd(kickoff, a.Session, sessionID)
}

// ResumeCmd returns an *exec.Cmd that resumes the agent with a new prompt.
// prompt is the revision/feedback text; sessionID is read from .agent.
func (a *Adapter) ResumeCmd(prompt, sessionID string) *exec.Cmd {
	if a.ResumeCmdTemplate != "" {
		return buildFromTemplate(a.ResumeCmdTemplate, map[string]string{
			"{prompt}":     prompt,
			"{session_id}": sessionID,
		})
	}
	resumeID := a.ResumeID
	if resumeID == "" {
		resumeID = a.Session
	}
	return a.buildStructuredCmd(prompt, resumeID, sessionID)
}

// effectivePromptFlag returns the configured prompt flag, defaulting to "-p".
func (a *Adapter) effectivePromptFlag() string {
	if a.Prompt != "" {
		return a.Prompt
	}
	return "-p"
}

// buildStructuredCmd assembles an *exec.Cmd from the adapter's structured fields.
// text is the prompt/kickoff text; sessionFlag and sessionID are appended when
// sessionFlag is non-empty and SessionType is not "directory".
func (a *Adapter) buildStructuredCmd(text, sessionFlag, sessionID string) *exec.Cmd {
	parts := strings.Fields(a.Binary)
	args := make([]string, 0, len(parts)+4)
	args = append(args, parts[1:]...)
	args = append(args, a.effectivePromptFlag(), text)
	if sessionFlag != "" && a.SessionType != "directory" {
		args = append(args, sessionFlag, sessionID)
	}
	return exec.Command(parts[0], args...)
}

// CheckBinary verifies that the adapter's binary is available on PATH.
// Returns a clear, actionable error (including the install hint when set)
// if the binary is not found.
func (a *Adapter) CheckBinary() error {
	binary := strings.Fields(a.Binary)[0]
	if _, err := exec.LookPath(binary); err != nil {
		if a.Install != "" {
			return fmt.Errorf("agent binary %q not found on PATH\ninstall it with: %s", binary, a.Install)
		}
		return fmt.Errorf("agent binary %q not found on PATH", binary)
	}
	return nil
}

// buildFromTemplate splits template on whitespace and replaces tokens that
// exactly match a placeholder key with the corresponding value. Values with
// spaces are passed as a single argument to exec.Command — no shell involved.
func buildFromTemplate(template string, replacements map[string]string) *exec.Cmd {
	tokens := strings.Fields(template)
	args := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if v, ok := replacements[tok]; ok {
			args = append(args, v)
		} else {
			args = append(args, tok)
		}
	}
	if len(args) == 0 {
		return exec.Command("")
	}
	return exec.Command(args[0], args[1:]...)
}

// LoadBytes parses YAML content into an Adapter and validates required fields.
// source is a descriptive label used in error messages (e.g. the file path).
// This is exported for use in tests that load fixtures directly.
func LoadBytes(data []byte, source string) (*Adapter, error) {
	return load(data, source)
}

// load parses YAML content into an Adapter and validates required fields.
func load(data []byte, source string) (*Adapter, error) {
	var a Adapter
	if err := yaml.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("invalid YAML in %s: %w", source, err)
	}
	if a.Binary == "" {
		return nil, fmt.Errorf("adapter %s: missing required field 'binary'", source)
	}
	a.source = source
	return &a, nil
}

// Get resolves the named adapter using the three-level lookup (first match wins):
//  1. Project-local  — .agentctl/adapters/<name>.yml[a] (relative to cwd)
//  2. User-level     — ~/.config/agentctl/adapters/<name>.yml[a]
//  3. Built-in       — embedded in the binary
//
// Both .yml and .yaml are accepted. When both exist in the same directory, .yml
// wins and a warning is printed to stderr.
func Get(name string) (*Adapter, error) {
	// 1. Project-local
	if data, src, ok := readFromDir(filepath.Join(".agentctl", "adapters"), name); ok {
		return load(data, src)
	}

	// 2. User-level
	if cfgDir, err := os.UserConfigDir(); err == nil {
		dir := filepath.Join(cfgDir, "agentctl", "adapters")
		if data, src, ok := readFromDir(dir, name); ok {
			return load(data, src)
		}
	}

	// 3. Built-in
	return loadBuiltin(name)
}

// List returns the deduplicated names of all available adapters, merging all
// three levels. User-defined adapters shadow built-in adapters with the same name.
func List() []string {
	seen := make(map[string]struct{})
	var names []string

	add := func(name string) {
		if _, exists := seen[name]; !exists {
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}

	// Project-local
	for _, n := range listDir(filepath.Join(".agentctl", "adapters")) {
		add(n)
	}

	// User-level
	if cfgDir, err := os.UserConfigDir(); err == nil {
		for _, n := range listDir(filepath.Join(cfgDir, "agentctl", "adapters")) {
			add(n)
		}
	}

	// Built-in
	for _, n := range listBuiltins() {
		add(n)
	}

	return names
}

// readFromDir looks for <dir>/<name>.yml or <dir>/<name>.yaml.
// Returns the file contents, the path, and true when found.
func readFromDir(dir, name string) ([]byte, string, bool) {
	ymlPath := filepath.Join(dir, name+".yml")
	yamlPath := filepath.Join(dir, name+".yaml")

	_, ymlErr := os.Stat(ymlPath)
	_, yamlErr := os.Stat(yamlPath)

	if ymlErr == nil && yamlErr == nil {
		fmt.Fprintf(os.Stderr, "warning: both %s and %s exist; using .yml\n", ymlPath, yamlPath)
		data, err := os.ReadFile(ymlPath)
		if err != nil {
			return nil, "", false
		}
		return data, ymlPath, true
	}
	if ymlErr == nil {
		data, err := os.ReadFile(ymlPath)
		if err != nil {
			return nil, "", false
		}
		return data, ymlPath, true
	}
	if yamlErr == nil {
		data, err := os.ReadFile(yamlPath)
		if err != nil {
			return nil, "", false
		}
		return data, yamlPath, true
	}
	return nil, "", false
}

// listDir returns the adapter names found in a directory (stem of .yml/.yaml files).
func listDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		var stem string
		switch {
		case strings.HasSuffix(n, ".yml"):
			stem = strings.TrimSuffix(n, ".yml")
		case strings.HasSuffix(n, ".yaml"):
			stem = strings.TrimSuffix(n, ".yaml")
		default:
			continue
		}
		if _, exists := seen[stem]; !exists {
			seen[stem] = struct{}{}
			names = append(names, stem)
		}
	}
	return names
}

// loadBuiltin loads a named adapter from the embedded built-in FS.
func loadBuiltin(name string) (*Adapter, error) {
	data, err := builtinFS.ReadFile("builtin/" + name + ".yml")
	if err != nil {
		all := List()
		if len(all) == 0 {
			return nil, fmt.Errorf("unknown adapter: %s. Available: none", name)
		}
		return nil, fmt.Errorf("unknown adapter: %s. Available: %s", name, strings.Join(all, " "))
	}
	return load(data, "builtin/"+name+".yml")
}

// listBuiltins returns the names of all embedded built-in adapters.
func listBuiltins() []string {
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yml") {
			names = append(names, strings.TrimSuffix(e.Name(), ".yml"))
		}
	}
	return names
}
