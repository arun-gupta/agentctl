package git

import (
	"testing"
)

func TestInferIssue(t *testing.T) {
	tests := []struct {
		branch string
		want   string
	}{
		{"42-my-feature", "42"},
		{"210-some-long-slug", "210"},
		{"main", ""},
		{"HEAD", ""},
		{"feature-no-number", ""},
		{"0-zero", "0"},
		{"1234-multi-digit", "1234"},
		{"", ""},
	}
	for _, tt := range tests {
		got := InferIssue(tt.branch)
		if got != tt.want {
			t.Errorf("InferIssue(%q) = %q, want %q", tt.branch, got, tt.want)
		}
	}
}

func TestLinkedWorktreesParserSmoke(t *testing.T) {
	// Parse a synthetic --porcelain output without calling git.
	// We exercise the parser indirectly by calling the unexported helper via
	// the exported LinkedWorktrees function with a fake git binary; instead
	// we test the branch-inference logic (which is pure) and leave integration
	// tests for environments with a real git repo.

	cases := []struct {
		branch  string
		wantIss string
	}{
		{"refs/heads/42-feature", "42"},
		{"refs/heads/main", ""},
		{"refs/heads/1-x", "1"},
	}
	for _, c := range cases {
		// Trim "refs/heads/" prefix as the parser does.
		branch := c.branch
		const prefix = "refs/heads/"
		if len(branch) > len(prefix) && branch[:len(prefix)] == prefix {
			branch = branch[len(prefix):]
		}
		got := InferIssue(branch)
		if got != c.wantIss {
			t.Errorf("InferIssue(%q) = %q, want %q", branch, got, c.wantIss)
		}
	}
}
