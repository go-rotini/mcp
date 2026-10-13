//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package mcp

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
)

// waitExited returns once the program has exited, while it is not yet reaped: its process
// group ID still names its group, so the final kill can't reach another one.
func TestWaitExitedLeavesTheProgramUnreaped(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 3")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if !waitExited(cmd.Process.Pid) {
		t.Fatal("waitExited couldn't wait")
	}
	// An unreaped group answers with success, or EPERM on macOS; a gone one with ESRCH.
	if err := syscall.Kill(-cmd.Process.Pid, 0); errors.Is(err, syscall.ESRCH) {
		t.Error("the exited program's group is gone before it was reaped")
	}
	if err := cmd.Wait(); cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 3 {
		t.Fatalf("Wait after waitExited: %v", err)
	}
	if err := syscall.Kill(-cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("the reaped program's group: %v, want ESRCH", err)
	}
}
