//go:build !windows

package mcp

import (
	"fmt"
	"os/exec"
	"syscall"
)

// terminate asks the process to stop with SIGTERM, which rotini's signal handling turns into a
// canceled context.
func terminate(cmd *exec.Cmd) error {
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("mcp: stop the program: %w", err)
	}
	return nil
}
