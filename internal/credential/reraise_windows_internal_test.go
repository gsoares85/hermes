//go:build windows

package credential

import (
	"os"
	"testing"
)

// Windows offers no way to deliver a signal to the current process, so reraise
// exits instead — with the number a shell reports for a program ended by
// Ctrl-C, which is how it says the same thing the Unix path says by dying.
func assertInterrupted(t *testing.T, state *os.ProcessState) {
	t.Helper()

	if got := state.ExitCode(); got != interruptedStatus {
		t.Errorf("the child exited with %d, want %d", got, interruptedStatus)
	}
}
