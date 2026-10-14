//go:build !unix && !windows

package mcp

import (
	"os"
	"strconv"
)

// alive reports whether the process pid is still running, where the system lists processes
// under /proc.
func alive(pid int) bool {
	_, err := os.Stat("/proc/" + strconv.Itoa(pid))
	return err == nil
}
