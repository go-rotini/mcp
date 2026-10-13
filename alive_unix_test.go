//go:build unix

package mcp

import (
	"bytes"
	"os"
	"runtime"
	"strconv"
	"syscall"
)

// alive reports whether the process pid is still running. On Linux a killed process that no
// one has reaped yet, as in a container without an init process, is not running.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	if runtime.GOOS != "linux" {
		return true
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// pid (comm) state …; comm may hold spaces and parentheses.
	_, rest, ok := bytes.CutLast(stat, []byte(")"))
	if fields := bytes.Fields(rest); ok && len(fields) > 0 {
		return string(fields[0]) != "Z" && string(fields[0]) != "X"
	}
	return true
}
