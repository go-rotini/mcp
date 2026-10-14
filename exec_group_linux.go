package mcp

import (
	"bytes"
	"os"
	"strconv"
)

// exitedRefuseSignals is false: on Linux a process that has exited but isn't reaped accepts
// signals, so EPERM means a running process refused one.
const exitedRefuseSignals = false

// groupScan keeps, between polls of one process group, the processes found running in it.
type groupScan struct {
	pids []string
}

// runningInGroup reports whether a process of the process group pgid is running, read from
// /proc: a process that has exited but isn't reaped still belongs to its group here. It
// reports true when /proc can't be read.
//
// While a process found by an earlier poll still runs, only those processes are read again.
// Once none does, all of /proc is scanned, since one may have started another before it
// exited.
func runningInGroup(pgid int, scan *groupScan) bool {
	want := []byte(strconv.Itoa(pgid))
	kept := scan.pids[:0]
	for _, pid := range scan.pids {
		if runningMember(pid, want) {
			kept = append(kept, pid)
		}
	}
	scan.pids = kept
	if len(kept) > 0 {
		return true
	}
	d, err := os.Open("/proc")
	if err != nil {
		return true
	}
	defer func() { _ = d.Close() }()
	names, err := d.Readdirnames(-1)
	if err != nil {
		return true
	}
	for _, name := range names {
		if name[0] >= '0' && name[0] <= '9' && runningMember(name, want) {
			scan.pids = append(scan.pids, name)
		}
	}
	return len(scan.pids) > 0
}

// runningMember reports whether the process pid belongs to the process group pgid and is
// running. A process whose main thread has exited shows as a zombie while its other threads
// run; it counts as running.
func runningMember(pid string, pgid []byte) bool {
	stat, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return false // exited and reaped
	}
	// pid (comm) state ppid pgrp …; comm may hold spaces and parentheses.
	_, rest, ok := bytes.CutLast(stat, []byte(")"))
	if !ok {
		return false
	}
	fields := bytes.Fields(rest)
	if len(fields) < 3 || !bytes.Equal(fields[2], pgid) {
		return false
	}
	switch state := fields[0]; {
	case len(state) != 1:
		return true
	case state[0] == 'X':
		return false
	case state[0] == 'Z':
		return threadsLeft(pid)
	}
	return true
}

// threadsLeft reports whether the zombie process pid has a thread besides its main one.
func threadsLeft(pid string) bool {
	d, err := os.Open("/proc/" + pid + "/task")
	if err != nil {
		return false
	}
	defer func() { _ = d.Close() }()
	tids, err := d.Readdirnames(2)
	return err == nil && len(tids) > 1
}
