//go:build windows

package mcp

import (
	"fmt"
	"os/exec"
)

// terminate kills the process: Windows can't send it SIGTERM, so rotini's signal handling
// doesn't run there.
func terminate(cmd *exec.Cmd) error {
	if err := cmd.Process.Kill(); err != nil {
		return fmt.Errorf("mcp: stop the program: %w", err)
	}
	return nil
}
