//go:build unix && !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package mcp

// waitExited can't wait for a process without reaping it on this system.
func waitExited(int) bool { return false }
