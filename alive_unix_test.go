//go:build !windows

package mcp

import "syscall"

// alive reports whether the process pid is still running.
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }
