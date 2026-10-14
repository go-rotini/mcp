//go:build unix && !linux

package mcp

// exitedRefuseSignals is true: a process group whose processes have all exited, but aren't
// reaped, refuses signals with EPERM. That is checked on macOS only; where another system
// accepts the signal instead, a stopped run's processes are killed when the grace period ends
// rather than as soon as they have exited.
const exitedRefuseSignals = true

// groupScan holds nothing: runningInGroup doesn't look at the group's processes.
type groupScan struct{}

// runningInGroup reports true: kill(2) has already found a process of the group that accepts
// signals, and such a process is running.
func runningInGroup(int, *groupScan) bool { return true }
