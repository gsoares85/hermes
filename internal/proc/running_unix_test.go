//go:build !windows

package proc

import (
	"os"
	"syscall"
)

// running asks whether a process is there, without touching it.
//
// Signal 0 is the question with no side effect: the kernel checks that the
// process exists and that this one may signal it, and delivers nothing.
func running(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	return process.Signal(syscall.Signal(0)) == nil
}
