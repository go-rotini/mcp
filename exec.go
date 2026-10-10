package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// errOutputLimit stops a run whose stdout or stderr passed the output limit.
var errOutputLimit = errors.New("output limit exceeded")

// run is one finished process.
type run struct {
	stdout, stderr []byte
	code           int
	timedOut       bool
	overLimit      bool
}

// execute runs the program for a call. Canceling ctx (the client canceled the call) or the
// timeout sends the process SIGTERM, so rotini's signal handling runs, and kills it after the
// grace period. Stdin is the call's stdin bytes, never the server's own stdin, which is the MCP
// transport.
func (s *server) execute(ctx context.Context, c *Call) (*run, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if s.cfg.timeout > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeoutCause(ctx, s.cfg.timeout, context.DeadlineExceeded)
		defer stop()
	}
	cmd := exec.CommandContext(ctx, s.exe, c.Argv...)
	cmd.Cancel = func() error { return terminate(cmd) }
	cmd.WaitDelay = s.cfg.grace
	cmd.Env = c.Env
	cmd.Dir = s.cfg.dir
	cmd.Stdin = bytes.NewReader(c.Stdin)
	stdout := &cappedBuffer{limit: s.cfg.outputLimit, over: func() { cancel(errOutputLimit) }}
	stderr := &cappedBuffer{limit: s.cfg.outputLimit, over: func() { cancel(errOutputLimit) }}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	r := &run{stdout: stdout.bytes(), stderr: stderr.bytes(), code: -1}
	if cmd.ProcessState != nil {
		r.code = cmd.ProcessState.ExitCode()
	}
	switch cause := context.Cause(ctx); {
	case errors.Is(cause, errOutputLimit):
		r.overLimit = true
		return r, nil
	case errors.Is(cause, context.DeadlineExceeded) && s.cfg.timeout > 0:
		r.timedOut = true
		return r, nil
	}
	if _, ok := errors.AsType[*exec.ExitError](err); err != nil && !ok {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("mcp: %w", context.Cause(ctx))
		}
		return nil, fmt.Errorf("mcp: run %s: %w", s.exe, err)
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

// defaultGrace is how long a canceled process has to exit after SIGTERM before it is killed.
const defaultGrace = 5 * time.Second

// defaultOutputLimit caps each of a run's stdout and stderr.
const defaultOutputLimit = 10 << 20
