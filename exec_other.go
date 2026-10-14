//go:build !unix && !windows

package mcp

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// group is the processes of one run. This system has no process groups, so stopping the run
// reaches the program only.
type group struct{}

// prepare needs nothing on this system.
func (*group) prepare(*exec.Cmd) {}

// attach needs nothing on this system.
func (*group) attach(*exec.Cmd) {}

// exited reports false: this system can't wait for the program without reaping it.
func (*group) exited(*exec.Cmd) bool { return false }

// settle is never called, since exited reports false.
func (*group) settle(*exec.Cmd, time.Time) {}

// terminate asks the program to stop with SIGTERM, which rotini's signal handling turns into
// a canceled context.
func (*group) terminate(cmd *exec.Cmd) error { return stopErr(cmd.Process.Signal(syscall.SIGTERM)) }

// kill stops the program at once.
func (*group) kill(cmd *exec.Cmd) error { return stopErr(cmd.Process.Kill()) }

// close releases the group; there is nothing to release.
func (*group) close() {}

func stopErr(err error) error {
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("mcp: stop the program: %w", err)
	}
	return nil
}
