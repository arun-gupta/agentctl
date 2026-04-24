// Package process provides helpers for managing OS processes referenced by PID.
package process

import (
	"os"
	"strconv"
	"syscall"
)

// IsAlive reports whether the process with the given PID is still running.
// It uses signal 0 (no-op) — same technique as `kill -0 $pid` in bash.
// Returns false for an empty or non-numeric pid string.
func IsAlive(pid string) bool {
	if pid == "" {
		return false
	}
	n, err := strconv.Atoi(pid)
	if err != nil || n <= 0 {
		return false
	}
	p, err := os.FindProcess(n)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// Kill sends SIGTERM to the process identified by pid.
// A non-numeric or empty pid is silently ignored.
// Errors (e.g. process already gone) are also silently swallowed, matching
// the `kill $pid 2>/dev/null || true` pattern in the shell script.
func Kill(pid string) {
	if pid == "" {
		return
	}
	n, err := strconv.Atoi(pid)
	if err != nil || n <= 0 {
		return
	}
	p, err := os.FindProcess(n)
	if err != nil {
		return
	}
	_ = p.Signal(syscall.SIGTERM)
}
