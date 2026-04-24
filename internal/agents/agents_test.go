package agents_test

import (
	"strings"
	"testing"

	"github.com/arun-gupta/agentctl/internal/agents"
)

var knownAdapters = []string{"claude", "codex", "copilot", "gemini", "opencode"}

func TestList(t *testing.T) {
	got := agents.List()
	if len(got) == 0 {
		t.Fatal("List returned no adapters")
	}
	index := make(map[string]bool, len(got))
	for _, name := range got {
		index[name] = true
	}
	for _, want := range knownAdapters {
		if !index[want] {
			t.Errorf("List missing expected adapter %q; got %v", want, got)
		}
	}
}

func TestRead_known(t *testing.T) {
	for _, name := range knownAdapters {
		b, err := agents.Read(name)
		if err != nil {
			t.Errorf("Read(%q) unexpected error: %v", name, err)
			continue
		}
		if len(b) == 0 {
			t.Errorf("Read(%q) returned empty content", name)
		}
		if !strings.Contains(string(b), "agent_launch") {
			t.Errorf("Read(%q) content missing agent_launch function", name)
		}
	}
}

func TestRead_unknown(t *testing.T) {
	_, err := agents.Read("nonexistent")
	if err == nil {
		t.Fatal("Read(nonexistent) expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("error message should mention unknown agent, got: %v", err)
	}
	for _, name := range knownAdapters {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error message should list available adapter %q, got: %v", name, err)
		}
	}
}
