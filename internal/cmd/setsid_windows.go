//go:build windows

package cmd

import "os/exec"

// detachProcess is a no-op on Windows; session detachment is not supported.
func detachProcess(_ *exec.Cmd) {}
