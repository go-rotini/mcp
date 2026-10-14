package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// errOutputLimit stops a run whose stdout or stderr passed the output limit.
var errOutputLimit = errors.New("output limit exceeded")

// errServerTimeout stops a run that passed the server's timeout.
var errServerTimeout = fmt.Errorf("the server's timeout: %w", context.DeadlineExceeded)

// run is one finished process.
type run struct {
	stdout, stderr []byte
	code           int
	timedOut       bool // stopped by a deadline
	serverTimeout  bool // that deadline was the server's timeout
	overLimit      bool
	failure        string // why the program couldn't be run, or ""
}

// execute runs the program for a call. Canceling ctx (the client canceled the call), a
// deadline on ctx or the timeout sends SIGTERM to the program and every process it started, so
// rotini's signal handling runs, and kills them after the grace period; with no grace period
// they are killed at once. Stdin is the call's stdin bytes, never the server's own stdin, which
// is the MCP transport. A program that can't be started is a run with failure set. A canceled
// call returns the cancellation as an error, however the stopped program exited; a run stopped
// by a deadline, or whose deadline passed before the program started, is a timed-out run.
//
// A run that exits while a process it started still holds its stdout or stderr open is
// finished once the grace period (or drainDelay) has passed: the result has the output read by
// then and the program's own exit status. Such a process is left running unless the run was
// stopped.
func (s *server) execute(ctx context.Context, c *Call) (*run, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if s.cfg.timeout > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeoutCause(ctx, s.cfg.timeout, errServerTimeout)
		defer stop()
	}
	g := &group{}
	cmd := exec.CommandContext(ctx, s.exe, c.Argv...)
	g.prepare(cmd)
	// termAt is when SIGTERM was sent, in Unix nanoseconds, so the grace period can be kept
	// after the program exits.
	var termAt atomic.Int64
	cmd.Cancel = func() error {
		termAt.Store(time.Now().UnixNano())
		return g.terminate(cmd)
	}
	cmd.WaitDelay = s.cfg.grace
	if s.cfg.grace <= 0 {
		cmd.Cancel = func() error { return g.kill(cmd) }
		cmd.WaitDelay = drainDelay
	}
	cmd.Env = c.Env
	cmd.Dir = s.cfg.dir
	cmd.Stdin = bytes.NewReader(c.Stdin)
	stdout := &cappedBuffer{limit: s.cfg.outputLimit, over: func() { cancel(errOutputLimit) }}
	stderr := &cappedBuffer{limit: s.cfg.outputLimit, over: func() { cancel(errOutputLimit) }}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Start(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &run{code: -1, timedOut: true, serverTimeout: errors.Is(context.Cause(ctx), errServerTimeout)}, nil
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("mcp: %w", context.Cause(ctx))
		}
		return &run{code: -1, failure: "the program couldn't be started: " + startError(err)}, nil
	}
	g.attach(cmd)
	// A stopped run's processes are all killed, so nothing it started outlives it. Where the
	// system allows, that happens while the program is exited but not yet reaped, when its
	// process group ID can't belong to another group: the processes it started keep what is
	// left of the grace period, and are killed once it ends.
	killed := false
	stop := func() {
		killed = true
		if err := g.kill(cmd); err != nil && s.log != nil {
			s.log.Warn("stopping a run", "err", err)
		}
	}
	if g.exited(cmd) && ctx.Err() != nil {
		if s.cfg.grace > 0 {
			start := time.Now()
			if at := termAt.Load(); at != 0 {
				start = time.Unix(0, at)
			}
			g.settle(cmd, start.Add(s.cfg.grace))
		}
		stop()
	}
	err := cmd.Wait()
	if ctx.Err() != nil && !killed {
		// Stopped after the program exited, while a process it started held its output open,
		// or on a system that can't wait without reaping. The group ID is safe to signal
		// while any process of the group lives; once none does, another group could take it
		// in the moment before this kill.
		stop()
	}
	g.close()
	r := &run{stdout: stdout.bytes(), stderr: stderr.bytes(), code: -1}
	if cmd.ProcessState != nil {
		r.code = cmd.ProcessState.ExitCode()
	}
	// A deadline is told by ctx.Err(), not by the cause, which a deadline set with
	// context.WithTimeoutCause or WithDeadlineCause replaces with its own.
	switch cause := context.Cause(ctx); {
	case errors.Is(cause, errOutputLimit):
		r.overLimit = true
		return r, nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		r.timedOut, r.serverTimeout = true, errors.Is(cause, errServerTimeout)
		return r, nil
	case cause != nil:
		// The call was canceled: how the stopped program exited is not its result.
		return nil, fmt.Errorf("mcp: %w", cause)
	}
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil {
		return r, nil // the program exited; a process it started kept the output open
	}
	if _, ok := errors.AsType[*exec.ExitError](err); err != nil && !ok {
		r.failure = "the program's run failed: " + err.Error()
	}
	return r, nil
}

// cappedBuffer keeps at most limit bytes and calls over once when more arrive. It reports every
// write as accepted, so the process isn't stopped by a broken pipe before it is terminated.
type cappedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int64
	over  func()
	hit   bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hit {
		return len(p), nil
	}
	if b.limit > 0 && int64(b.buf.Len()+len(p)) > b.limit {
		b.buf.Write(p[:b.limit-int64(b.buf.Len())])
		b.hit = true
		b.over()
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func (b *cappedBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}

// startError says why the program couldn't be started, without the path the server runs it
// from.
func startError(err error) string {
	if pe, ok := errors.AsType[*os.PathError](err); ok {
		return pe.Err.Error()
	}
	return err.Error()
}

// tail is at most the last n bytes of b, trimmed, starting at a whole line when one starts in
// them.
func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
		if i := bytes.IndexByte(b, '\n'); i >= 0 && i < len(b)-1 {
			b = b[i+1:]
		}
	}
	return strings.TrimSpace(string(b))
}

// defaultGrace is how long a canceled process has to exit after SIGTERM before it is killed.
const defaultGrace = 5 * time.Second

// drainDelay is how long, with no grace period, a run's output is still read after the process
// exits or is killed, while a process it started holds stdout or stderr open.
const drainDelay = time.Second

// defaultOutputLimit caps each of a run's stdout and stderr.
const defaultOutputLimit = 10 << 20
