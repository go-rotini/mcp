//go:build windows

package mcp

import "os/exec"

// terminate kills the process: Windows can't send it SIGTERM, so rotini's signal handling
// doesn't run there.
func terminate(cmd *exec.Cmd) error {
	return kill(cmd)
}
