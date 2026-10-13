package mcp

import (
	"errors"

	"golang.org/x/sys/unix"
)

// waitExited blocks until the process pid has exited, without reaping it, and reports
// whether it could.
func waitExited(pid int) bool {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(err, unix.EINTR) {
			return err == nil
		}
	}
}
