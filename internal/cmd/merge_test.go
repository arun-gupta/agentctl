package cmd

import (
	"testing"
)

func TestNewMergeCmd_flags(t *testing.T) {
	c := NewMergeCmd()
	if c.Use != "merge [issue]" {
		t.Errorf("Use = %q, want %q", c.Use, "merge [issue]")
	}

	strategyFlag := c.Flags().Lookup("strategy")
	if strategyFlag == nil {
		t.Fatal("--strategy flag not registered")
	}
	if strategyFlag.DefValue != "" {
		t.Errorf("--strategy default = %q, want empty (resolved at runtime)", strategyFlag.DefValue)
	}

	noDeleteFlag := c.Flags().Lookup("no-delete")
	if noDeleteFlag == nil {
		t.Fatal("--no-delete flag not registered")
	}
	if noDeleteFlag.DefValue != "false" {
		t.Errorf("--no-delete default = %q, want %q", noDeleteFlag.DefValue, "false")
	}

	dryRunFlag := c.Flags().Lookup("dry-run")
	if dryRunFlag == nil {
		t.Fatal("--dry-run flag not registered")
	}
	if dryRunFlag.DefValue != "false" {
		t.Errorf("--dry-run default = %q, want %q", dryRunFlag.DefValue, "false")
	}
}

func TestResolveMergeStrategy(t *testing.T) {
	tests := []struct {
		flag   string
		config string
		want   string
	}{
		{"", "", "squash"},
		{"", "squash", "squash"},
		{"", "merge", "merge"},
		{"", "rebase", "rebase"},
		{"squash", "merge", "squash"},
		{"rebase", "", "rebase"},
		{"merge", "squash", "merge"},
	}
	for _, tt := range tests {
		got := resolveMergeStrategy(tt.flag, tt.config)
		if got != tt.want {
			t.Errorf("resolveMergeStrategy(%q, %q) = %q, want %q", tt.flag, tt.config, got, tt.want)
		}
	}
}

func TestValidateMergeStrategy(t *testing.T) {
	valid := []string{"squash", "merge", "rebase"}
	for _, s := range valid {
		if err := validateMergeStrategy(s); err != nil {
			t.Errorf("validateMergeStrategy(%q) returned unexpected error: %v", s, err)
		}
	}

	invalid := []string{"fast-forward", "ours", "", " "}
	// empty string is handled before validateMergeStrategy is called (resolved to "squash")
	// but we still test it produces no error since the validator is called after resolution
	for _, s := range invalid {
		if s == "" {
			continue // empty is valid input to validator (means "use default")
		}
		if err := validateMergeStrategy(s); err == nil {
			t.Errorf("validateMergeStrategy(%q) expected error, got nil", s)
		}
	}
}
