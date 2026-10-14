//go:build windows

package mcp

import (
	"fmt"
	"os/exec"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// group is the processes of one run: the program and everything it starts, which a job object
// holds. A process the program starts before it is put in the job (in the moment between its
// start and attach) is not in the job.
type group struct {
	mu  sync.Mutex
	job windows.Handle
}

// prepare needs nothing before the start on Windows: the job is made in attach.
func (*group) prepare(*exec.Cmd) {}

// attach puts the started program in a new job object, which the processes it starts join.
// When that fails, stopping the run reaches the program only.
func (g *group) attach(cmd *exec.Cmd) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if windows.AssignProcessToJobObject(job, h) != nil {
		_ = windows.CloseHandle(job)
		return
	}
	g.mu.Lock()
	g.job = job
	g.mu.Unlock()
}

// exited reports false: a job object is reached by its handle, so stopping the run after the
// program is reaped is safe, and there is nothing to wait for first.
func (*group) exited(*exec.Cmd) bool { return false }

// settle is never called, since exited reports false.
func (*group) settle(*exec.Cmd, time.Time) {}

// terminate kills every process of the run: Windows can't send SIGTERM, so rotini's signal
// handling doesn't run there.
func (g *group) terminate(cmd *exec.Cmd) error { return g.kill(cmd) }

// kill stops every process of the run at once.
func (g *group) kill(cmd *exec.Cmd) error {
	g.mu.Lock()
	job := g.job
	g.mu.Unlock()
	if job != 0 && windows.TerminateJobObject(job, 1) == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil {
		return fmt.Errorf("mcp: stop the program: %w", err)
	}
	return nil
}

// close releases the job object. The processes still in it keep running.
func (g *group) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job != 0 {
		_ = windows.CloseHandle(g.job)
		g.job = 0
	}
}
