package main

import "testing"

// TestRootCmdSilenceErrors verifies that the root command suppresses cobra's
// built-in error printing so errors are not echoed twice (once by cobra, once
// by main's fmt.Fprintln).
func TestRootCmdSilenceErrors(t *testing.T) {
	root := newRootCmd()
	if !root.SilenceErrors {
		t.Error("root command must set SilenceErrors: true to prevent cobra from printing errors a second time after main already prints them")
	}
}
