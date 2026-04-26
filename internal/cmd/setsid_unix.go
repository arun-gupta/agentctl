//go:build !windows

package cmd

import (
	"os/exec"
	"syscall"
)

// detachProcess configures cmd to run in a new session (equivalent to setsid),
// detaching it from the controlling terminal so it survives after the parent exits.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
