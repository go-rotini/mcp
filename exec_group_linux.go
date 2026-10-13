package mcp

import (
	"bytes"
	"os"
	"strconv"
)

// exitedRefuseSignals is false: on Linux a process that has exited but isn't reaped accepts
// signals, so EPERM means a running process refused one.
const exitedRefuseSignals = false

// runningInGroup reports whether a process of the process group pgid is running, read from
// /proc: a process that has exited but isn't reaped still belongs to its group here. It
// reports true when /proc can't be read.
func runningInGroup(pgid int) bool {
	d, err := os.Open("/proc")
	if err != nil {
		return true
	}
	defer func() { _ = d.Close() }()
	names, err := d.Readdirnames(-1)
	if err != nil {
		return true
	}
	want := []byte(strconv.Itoa(pgid))
	for _, name := range names {
		if name[0] < '0' || name[0] > '9' {
			continue
		}
		stat, err := os.ReadFile("/proc/" + name + "/stat")
		if err != nil {
			continue // exited since the directory was read
		}
		// pid (comm) state ppid pgrp …; comm may hold spaces and parentheses.
		_, rest, ok := bytes.CutLast(stat, []byte(")"))
		if !ok {
			continue
		}
		fields := bytes.Fields(rest)
		if len(fields) < 3 || !bytes.Equal(fields[2], want) {
			continue
		}
		if state := fields[0]; len(state) == 1 && (state[0] == 'Z' || state[0] == 'X') {
			continue
		}
		return true
	}
	return false
}
