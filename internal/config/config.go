// Package config handles reading and writing the per-repo .agentctl.yml file
// as well as the user-level global config at $XDG_CONFIG_HOME/agentctl/config.yml.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/arun-gupta/agentctl/internal/xdg"
)

const Filename = ".agentctl.yml"

// SDDNone is the sentinel value for explicitly disabling SDD as the default.
// It is distinct from the empty string ("not set") so that a project-local
// default_sdd: none can override a global default_sdd: speckit.
const SDDNone = "none"

// VCSConfig holds optional VCS provider overrides. Useful for self-hosted
// GitLab instances where the origin URL does not contain "gitlab.com".
type VCSConfig struct {
	// Provider overrides automatic detection. Valid values: "github", "gitlab".
	Provider string `yaml:"provider,omitempty"`
	// Server is the base URL of a self-hosted instance (e.g. https://gitlab.mycompany.com).
	Server string `yaml:"server,omitempty"`
}

// AgentctlConfig is the schema for .agentctl.yml.
type AgentctlConfig struct {
	// DevServer is the command to start the project's dev server.
	// Use {port} as a placeholder; agentctl substitutes the allocated port.
	// Example: "uvicorn main:app --port {port}"
	DevServer string `yaml:"dev_server,omitempty"`

	// Notify enables native desktop notifications when a headless agent
	// finishes. Only meaningful in headless mode; ignored for foreground runs.
	// When true, agentctl fires an OS notification (macOS: osascript,
	// Linux: notify-send) after each agent exits. Can also be enabled
	// per-invocation with the --notify flag.
	// Uses *bool so that an explicit false in project-local config can
	// override a global notify: true.
	Notify *bool `yaml:"notify,omitempty"`

	// MergeStrategy is the default merge strategy used by agentctl merge.
	// Valid values: "squash" (default when empty), "merge", "rebase".
	// Override per-invocation with the --strategy flag.
	MergeStrategy string `yaml:"merge_strategy,omitempty"`

	// TestCmd is the command to run the project's automated test suite.
	// When set, agents use this instead of inferring the command from AGENTS.md
	// or README.md. Example: "go test ./...", "npm test", "pytest"
	TestCmd string `yaml:"test_cmd,omitempty"`

	// Editor is the program used by agentctl open to open a worktree.
	// Overridden per-invocation by the --editor flag.
	// Example: "cursor", "code", "idea"
	Editor string `yaml:"editor,omitempty"`

	// DefaultAgent overrides the built-in "claude" default for agentctl start.
	// Any installed adapter name is valid (e.g. "codex", "copilot").
	// Overridden per-invocation by the --agent flag.
	DefaultAgent string `yaml:"default_agent,omitempty"`

	// DefaultSDD sets the default SDD methodology for agentctl start when
	// --sdd is not passed. Valid values: "plain", "speckit", a custom name,
	// or the sentinel "none" (config.SDDNone) to explicitly disable SDD.
	// Empty string means "not set" and falls through to the built-in default (no SDD).
	DefaultSDD string `yaml:"default_sdd,omitempty"`

	// BuildCmd is the command used by agentctl build to compile the project.
	// Use {output} as a placeholder for the output binary path.
	// Example: "go build -o {output} ./cmd/myapp"
	BuildCmd string `yaml:"build_cmd,omitempty"`

	// VCS holds optional VCS provider overrides for self-hosted instances.
	VCS VCSConfig `yaml:"vcs,omitempty"`

	// Diagnostics controls per-run structured metadata collection.
	// Set enabled: false to disable writing run records to .agentctl/runs/.
	Diagnostics DiagnosticsConfig `yaml:"diagnostics,omitempty"`
}

// DiagnosticsConfig controls per-run structured metadata collection.
type DiagnosticsConfig struct {
	// Enabled controls whether per-run diagnostics are written.
	// Defaults to true when the field is absent.
	Enabled *bool `yaml:"enabled,omitempty"`
}

// GlobalConfig holds only the user-wide settings that belong in the global
// config file. Using a dedicated struct prevents accidentally reading or
// writing repo-specific fields through the global path. A new field added to
// AgentctlConfig is not silently included in global config without an explicit
// decision here.
type GlobalConfig struct {
	DefaultAgent  string            `yaml:"default_agent,omitempty"`
	DefaultSDD    string            `yaml:"default_sdd,omitempty"`
	Notify        *bool             `yaml:"notify,omitempty"`
	MergeStrategy string            `yaml:"merge_strategy,omitempty"`
	Editor        string            `yaml:"editor,omitempty"`
	Diagnostics   DiagnosticsConfig `yaml:"diagnostics,omitempty"`
}

// GlobalPath returns the path to the user-level global config file:
// $XDG_CONFIG_HOME/agentctl/config.yml (or the OS default via xdg.UserConfigDir).
func GlobalPath() string {
	dir := xdg.UserConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "agentctl", "config.yml")
}

// ReadGlobal loads the global config file at GlobalPath().
// Returns an empty GlobalConfig (no error) when the file does not exist.
// Returns an error if the file exists but contains invalid YAML.
func ReadGlobal() (*GlobalConfig, error) {
	path := GlobalPath()
	if path == "" {
		return &GlobalConfig{}, nil
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &GlobalConfig{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg GlobalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// WriteGlobal serialises cfg to GlobalPath(), creating parent directories as needed.
func WriteGlobal(cfg *GlobalConfig) error {
	path := GlobalPath()
	if path == "" {
		return fmt.Errorf("global config path unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ReadMerged returns the effective config by merging the global config and the
// project-local .agentctl.yml. Project-local values win on any non-zero field;
// the global config fills in the rest. Repo-specific fields (dev_server,
// test_cmd, build_cmd, vcs) are only read from the project-local file.
//
// Error behaviour: a missing global config is silently skipped. If the global
// config file exists but is malformed, ReadMerged returns an error.
func ReadMerged(dir string) (*AgentctlConfig, error) {
	global, err := ReadGlobal()
	if err != nil {
		return nil, fmt.Errorf("global config: %w", err)
	}

	local, err := Read(dir)
	if err != nil {
		return nil, err
	}

	return &AgentctlConfig{
		// Global-eligible fields: project-local wins on non-zero value.
		DefaultAgent:  mergeString(local.DefaultAgent, global.DefaultAgent),
		DefaultSDD:    mergeString(local.DefaultSDD, global.DefaultSDD),
		Notify:        mergeBoolPtr(local.Notify, global.Notify),
		MergeStrategy: mergeString(local.MergeStrategy, global.MergeStrategy),
		Editor:        mergeString(local.Editor, global.Editor),
		Diagnostics:   mergeDiagnostics(local.Diagnostics, global.Diagnostics),

		// Repo-specific fields: always from project-local only.
		DevServer: local.DevServer,
		TestCmd:   local.TestCmd,
		BuildCmd:  local.BuildCmd,
		VCS:       local.VCS,
	}, nil
}

// mergeString returns local if non-empty, otherwise global.
func mergeString(local, global string) string {
	if local != "" {
		return local
	}
	return global
}

// mergeBoolPtr returns local if non-nil, otherwise global.
func mergeBoolPtr(local, global *bool) *bool {
	if local != nil {
		return local
	}
	return global
}

// mergeDiagnostics merges two DiagnosticsConfig values: local wins for non-nil fields.
func mergeDiagnostics(local, global DiagnosticsConfig) DiagnosticsConfig {
	return DiagnosticsConfig{
		Enabled: mergeBoolPtr(local.Enabled, global.Enabled),
	}
}

// Read loads .agentctl.yml from dir. If the file does not exist, an empty
// config is returned with no error.
func Read(dir string) (*AgentctlConfig, error) {
	data, err := os.ReadFile(filepath.Join(dir, Filename))
	if os.IsNotExist(err) {
		return &AgentctlConfig{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg AgentctlConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Write serialises cfg to .agentctl.yml in dir, creating the file if needed.
func Write(dir string, cfg *AgentctlConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, Filename), data, 0o644)
}
