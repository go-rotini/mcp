//go:build unix

package mcp

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// group is the processes of one run: the program and everything it starts, which share the
// process group the program leads.
type group struct{}

// prepare starts the program in a process group of its own, so stopping the run reaches the
// processes it starts too.
func (*group) prepare(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// attach is called once the program has started; the process group needs nothing more.
func (*group) attach(*exec.Cmd) {}

// exited blocks until the program has exited, without reaping it, so its process group ID
// can't be taken by another group yet, and reports whether it could.
func (*group) exited(cmd *exec.Cmd) bool { return waitExited(cmd.Process.Pid) }

// settle waits, after exited, until no process of the run is left running or deadline
// passes. The program stays unreaped meanwhile, so its process group ID stays reserved.
func (*group) settle(cmd *exec.Cmd, deadline time.Time) {
	pgid := cmd.Process.Pid
	var scan groupScan
	for wait := 5 * time.Millisecond; !groupGone(pgid, &scan); wait = min(2*wait, 100*time.Millisecond) {
		left := time.Until(deadline)
		if left <= 0 {
			return
		}
		time.Sleep(min(wait, left))
	}
}

// groupGone reports whether no process of the process group pgid is left running; processes
// that have exited but aren't yet reaped don't count. scan carries what one poll learned to
// the next.
func groupGone(pgid int, scan *groupScan) bool {
	err := syscall.Kill(-pgid, 0)
	switch {
	case errors.Is(err, syscall.ESRCH):
		return true
	case errors.Is(err, syscall.EPERM):
		return exitedRefuseSignals
	case err != nil:
		return false
	}
	return !runningInGroup(pgid, scan)
}

// terminate asks every process of the run to stop with SIGTERM, which rotini's signal handling
// turns into a canceled context.
func (*group) terminate(cmd *exec.Cmd) error { return signalGroup(cmd, syscall.SIGTERM) }

// kill stops every process of the run at once.
func (*group) kill(cmd *exec.Cmd) error { return signalGroup(cmd, syscall.SIGKILL) }

// close releases the group; a process group holds nothing to release.
func (*group) close() {}

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	// ESRCH: the group is gone. EPERM, where exitedRefuseSignals: every process left in it
	// has exited and waits to be reaped.
	err := syscall.Kill(-cmd.Process.Pid, sig)
	if err != nil && !errors.Is(err, syscall.ESRCH) && (!exitedRefuseSignals || !errors.Is(err, syscall.EPERM)) {
		return fmt.Errorf("mcp: stop the program: %w", err)
	}
	return nil
}
