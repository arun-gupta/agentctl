package agents

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed scripts/*.sh
var embeddedFS embed.FS

// Read returns the content of the named adapter script.
// name is the bare adapter name without path or extension (e.g. "claude").
func Read(name string) ([]byte, error) {
	b, err := embeddedFS.ReadFile("scripts/" + name + ".sh")
	if err != nil {
		available := List()
		if len(available) == 0 {
			return nil, fmt.Errorf("unknown agent: %s. Available: none", name)
		}
		return nil, fmt.Errorf("unknown agent: %s. Available: %s", name, strings.Join(available, " "))
	}
	return b, nil
}

// List returns the base names of all embedded adapter scripts.
func List() []string {
	entries, err := fs.ReadDir(embeddedFS, "scripts")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sh") {
			names = append(names, strings.TrimSuffix(e.Name(), ".sh"))
		}
	}
	return names
}
