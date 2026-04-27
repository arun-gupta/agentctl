// Package sdd implements pluggable SDD (spec-driven development) methodology
// loading with a three-level resolution chain: project-local → user-level → built-in.
package sdd

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed builtin/*.yml
var builtinFS embed.FS

// genericSkipPrompt is the hardcoded prompt used when --no-sdd is set.
// It never varies by methodology.
const genericSkipPrompt = `Work on GitHub issue #{issue}. Read CLAUDE.md for project conventions.
Skip the SDD lifecycle — make the changes directly, push the branch,
and open a PR. Do not merge. Dev server is running on port {port}.`

// Methodology describes a single SDD lifecycle. The name is derived from the
// YAML filename stem (e.g. "speckit.yml" → "speckit"). There is no name: field.
type Methodology struct {
	// Kickoff is the full prompt sent to the agent at start time. Required.
	// Placeholders {issue} and {port} are substituted via strings.ReplaceAll.
	Kickoff string `yaml:"kickoff"`
}

// KickoffPrompt substitutes {issue} and {port} in the methodology's kickoff
// template and returns the resulting prompt.
func (m *Methodology) KickoffPrompt(issue, port string) string {
	s := strings.ReplaceAll(m.Kickoff, "{issue}", issue)
	s = strings.ReplaceAll(s, "{port}", port)
	return s
}

// SkipPrompt returns the generic skip prompt with {issue} and {port} substituted.
// This is always the same regardless of which methodology is active.
func SkipPrompt(issue, port string) string {
	s := strings.ReplaceAll(genericSkipPrompt, "{issue}", issue)
	s = strings.ReplaceAll(s, "{port}", port)
	return s
}

// Get resolves the named methodology using the three-level lookup (first match wins):
//  1. Project-local  — .agentctl/sdd/<name>.yml[a] (relative to cwd)
//  2. User-level     — ~/.config/agentctl/sdd/<name>.yml[a]
//  3. Built-in       — embedded in the binary
//
// Both .yml and .yaml are accepted. When both exist in the same directory, .yml
// wins and a warning is printed to stderr.
func Get(name string) (*Methodology, error) {
	// 1. Project-local
	if data, src, ok := readFromDir(filepath.Join(".agentctl", "sdd"), name); ok {
		return load(data, src)
	}

	// 2. User-level
	if cfgDir, err := os.UserConfigDir(); err == nil {
		dir := filepath.Join(cfgDir, "agentctl", "sdd")
		if data, src, ok := readFromDir(dir, name); ok {
			return load(data, src)
		}
	}

	// 3. Built-in
	return loadBuiltin(name)
}

// List returns the deduplicated names of all available methodologies, merging
// all three levels. User-defined methodologies shadow built-ins with the same name.
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
	for _, n := range listDir(filepath.Join(".agentctl", "sdd")) {
		add(n)
	}

	// User-level
	if cfgDir, err := os.UserConfigDir(); err == nil {
		for _, n := range listDir(filepath.Join(cfgDir, "agentctl", "sdd")) {
			add(n)
		}
	}

	// Built-in
	for _, n := range listBuiltins() {
		add(n)
	}

	return names
}

// load parses YAML content into a Methodology and validates required fields.
func load(data []byte, source string) (*Methodology, error) {
	var m Methodology
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid YAML in %s: %w", source, err)
	}
	if m.Kickoff == "" {
		return nil, fmt.Errorf("methodology %s: missing required field 'kickoff'", source)
	}
	return &m, nil
}

// LoadBytes parses YAML content into a Methodology and validates required fields.
// source is a descriptive label used in error messages (e.g. the file path).
// This is exported for use in tests that load fixtures directly.
func LoadBytes(data []byte, source string) (*Methodology, error) {
	return load(data, source)
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

// listDir returns the methodology names found in a directory (stem of .yml/.yaml files).
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

// loadBuiltin loads a named methodology from the embedded built-in FS.
func loadBuiltin(name string) (*Methodology, error) {
	data, err := builtinFS.ReadFile("builtin/" + name + ".yml")
	if err != nil {
		available := List()
		return nil, fmt.Errorf(
			"unknown SDD methodology %q (available: %s) — or drop %s.yml in .agentctl/sdd/ or ~/.config/agentctl/sdd/",
			name, strings.Join(available, ", "), name,
		)
	}
	return load(data, "builtin/"+name+".yml")
}

// listBuiltins returns the names of all embedded built-in methodologies.
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
