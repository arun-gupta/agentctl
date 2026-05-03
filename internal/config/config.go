// Package config handles reading and writing the per-repo .agentctl.yml file.
package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const Filename = ".agentctl.yml"

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
	Notify bool `yaml:"notify,omitempty"`

	// MergeStrategy is the default merge strategy used by agentctl merge.
	// Valid values: "squash" (default when empty), "merge", "rebase".
	// Override per-invocation with the --strategy flag.
	MergeStrategy string `yaml:"merge_strategy,omitempty"`

	// VCS holds optional VCS provider overrides for self-hosted instances.
	VCS VCSConfig `yaml:"vcs,omitempty"`
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
