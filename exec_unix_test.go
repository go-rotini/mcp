//go:build unix

package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A process the program started keeps the grace period after the program itself exits on
// SIGTERM, and the run doesn't wait longer than the processes need.
func TestExecute_childrenKeepTheGracePeriod(t *testing.T) {
	mark := filepath.Join(t.TempDir(), "cleaned")
	script := `( trap 'sleep 0.5; echo ok > "` + mark + `"; exit 0' TERM; while :; do sleep 0.05; done ) &
trap 'exit 0' TERM
echo ready
while :; do sleep 0.05; done`
	s := &server{cfg: &config{grace: 5 * time.Second, outputLimit: defaultOutputLimit}, exe: "/bin/sh"}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	_, err := s.execute(ctx, &Call{Argv: []string{"-c", script}, Env: os.Environ()})
	took := time.Since(start)
	if err == nil {
		t.Fatal("a canceled run returned no error")
	}
	if _, err := os.Stat(mark); err != nil {
		t.Errorf("the child was killed before its cleanup, %v into a 5s grace period", took-300*time.Millisecond)
	}
	if took > 3*time.Second {
		t.Errorf("the run took %v; its processes were all gone after about 0.8s", took)
	}
}

// A deadline on the call's context, such as one a middleware sets, stops the run like the
// server's timeout: the result keeps the output tail and names the deadline.
func TestExecute_callDeadlineIsATimeout(t *testing.T) {
	s := &server{cfg: &config{grace: time.Second, outputLimit: defaultOutputLimit}, exe: "/bin/sh"}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	r, err := s.execute(ctx, &Call{Argv: []string{"-c", "echo partial >&2; sleep 5"}, Env: os.Environ()})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !r.timedOut || r.serverTimeout {
		t.Fatalf("timedOut %v, serverTimeout %v", r.timedOut, r.serverTimeout)
	}
	res := (&tool{}).result(r)
	if txt := text(res); !strings.Contains(txt, "ran past the call's deadline") || !strings.Contains(txt, "partial") {
		t.Errorf("result text %q", txt)
	}

	s.cfg.timeout = 200 * time.Millisecond
	r, err = s.execute(context.Background(), &Call{Argv: []string{"-c", "sleep 5"}, Env: os.Environ()})
	if err != nil || !r.timedOut || !r.serverTimeout {
		t.Fatalf("server timeout: %+v, %v", r, err)
	}
}
