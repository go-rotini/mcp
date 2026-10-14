//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package mcp

import (
	"errors"

	"golang.org/x/sys/unix"
)

// waitExited blocks until the process pid has exited, without reaping it, and reports
// whether it could.
func waitExited(pid int) bool {
	kq, err := unix.Kqueue()
	if err != nil {
		return false
	}
	defer func() { _ = unix.Close(kq) }()
	var change, event [1]unix.Kevent_t
	unix.SetKevent(&change[0], pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	change[0].Fflags = unix.NOTE_EXIT
	for {
		n, err := unix.Kevent(kq, change[:], event[:], nil)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.ESRCH):
			return true // exited already; the caller hasn't reaped it
		case err != nil || n == 0:
			return false
		case event[0].Flags&unix.EV_ERROR != 0:
			return event[0].Data == int64(unix.ESRCH)
		}
		return true
	}
}
