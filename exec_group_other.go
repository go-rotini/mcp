//go:build unix && !linux

package mcp

// exitedRefuseSignals is true: on macOS a process group whose processes have all exited, but
// aren't reaped, refuses signals with EPERM.
const exitedRefuseSignals = true

// runningInGroup reports true: kill(2) has already found a process of the group that accepts
// signals, and such a process is running.
func runningInGroup(int) bool { return true }
