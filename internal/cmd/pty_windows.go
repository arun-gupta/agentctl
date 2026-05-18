//go:build windows

package cmd

import (
	"fmt"
	"os"
	"os/exec"
)

func startWithPTY(cmd *exec.Cmd) (*os.File, error) {
	return nil, fmt.Errorf("pty mode is not supported on Windows")
}
