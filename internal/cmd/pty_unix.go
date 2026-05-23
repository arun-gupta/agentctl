//go:build !windows

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

func startWithPTY(cmd *exec.Cmd) (*os.File, error) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		return nil, fmt.Errorf("pty.Open: %w", err)
	}
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}
	if err := cmd.Start(); err != nil {
		tty.Close()
		ptmx.Close()
		return nil, err
	}
	tty.Close()
	return ptmx, nil
}
