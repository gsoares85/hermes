//go:build !windows

package credential

import (
	"os"
	"syscall"
	"testing"
)

// Killed by the signal, not exited with a status. That is the whole difference:
// a shell reports 128+n and a supervisor sees a termination rather than a
// program that decided to stop.
func assertInterrupted(t *testing.T, state *os.ProcessState) {
	t.Helper()

	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("this system does not report how the child ended: %v", state)
	}

	if !status.Signaled() {
		t.Fatalf("the child exited with status %d, want it killed by the signal", state.ExitCode())
	}
	if got := status.Signal(); got != syscall.SIGINT {
		t.Errorf("the child was killed by %v, want %v", got, syscall.SIGINT)
	}
}
