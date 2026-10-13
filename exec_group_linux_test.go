package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A process whose main thread has exited while another thread runs keeps the grace period.
func TestExecute_zombieLeaderWithThreadsKeepsTheGracePeriod(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	bin := filepath.Join(t.TempDir(), "leaderexit")
	if out, err := exec.Command(cc, "-pthread", "-o", bin, "testdata/leaderexit/main.c").CombinedOutput(); err != nil {
		t.Skipf("can't build the fixture: %v\n%s", err, out)
	}
	mark := filepath.Join(t.TempDir(), "cleaned")
	script := `"` + bin + `" "` + mark + `" &
trap 'exit 0' TERM
while :; do sleep 0.05; done`
	s := &server{cfg: &config{grace: 5 * time.Second, outputLimit: defaultOutputLimit}, exe: "/bin/sh"}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	if _, err := s.execute(ctx, &Call{Argv: []string{"-c", script}, Env: os.Environ()}); err == nil {
		t.Fatal("a canceled run returned no error")
	}
	took := time.Since(start)
	if _, err := os.Stat(mark); err != nil {
		t.Errorf("the process was killed before its cleanup, %v into a 5s grace period", took-300*time.Millisecond)
	}
	if took > 3*time.Second {
		t.Errorf("the run took %v; its processes were all gone after about 0.8s", took)
	}
}

// After the first scan, a poll reads only the processes it found while one of them runs, and
// a process started since is found once they have all exited.
func TestRunningInGroup_rescansWhenKnownProcessesExit(t *testing.T) {
	first := exec.Command("sleep", "30")
	first.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := first.Process.Pid
	defer func() { _ = first.Process.Kill(); _ = first.Wait() }()
	var scan groupScan
	if !runningInGroup(pgid, &scan) || len(scan.pids) != 1 {
		t.Fatalf("first scan: pids %v", scan.pids)
	}
	second := exec.Command("sleep", "30")
	second.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Process.Kill(); _ = second.Wait() }()
	_ = first.Process.Kill()
	_ = first.Wait()
	if !runningInGroup(pgid, &scan) {
		t.Fatal("the process started after the first scan wasn't found")
	}
	_ = second.Process.Kill()
	_ = second.Wait()
	if runningInGroup(pgid, &scan) {
		t.Fatal("an empty group reported running")
	}
}
