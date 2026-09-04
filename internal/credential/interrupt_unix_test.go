//go:build !windows

package credential_test

import (
	"syscall"
	"testing"
	"time"
)

// The test that was missing, and the reason the defect shipped: everything
// written about the signal handler proved that the files go, and nothing proved
// that the process still dies.
//
// signal.Notify disarms the default disposition for the whole process. A
// handler that cleans up and returns without restoring it leaves the program
// unkillable by Ctrl-C — the first interrupt runs the cleanup, and every one
// after it lands in a channel nobody reads.
//
// Unix only, because Windows offers no way to deliver a signal to another
// process; there the guarantee is asserted by the exit that reraise performs.
func TestAnInterruptStillEndsTheProcess(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()

	child := newHelper(t, waitForInterrupt, helperDirectory+"="+directory)
	child.start(t)

	// The child reports once it is watching, so the signal cannot arrive
	// before the handler is installed.
	var watching string
	child.reported(t, &watching)

	if left := filesIn(t, directory); len(left) != 1 {
		t.Fatalf("the child reported %q but left %d files in %s", watching, len(left), directory)
	}

	if err := child.command.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("interrupting the child: %v", err)
	}

	finished := make(chan error, 1)
	go func() { finished <- child.command.Wait() }()

	select {
	case err := <-finished:
		// Dying from the signal is the outcome. A clean exit status zero would
		// mean the handler swallowed the interrupt and returned normally.
		if err == nil {
			t.Error("the child exited cleanly after SIGINT, so the signal was swallowed")
		}
	case <-time.After(10 * time.Second):
		_ = child.command.Process.Kill()
		t.Fatal("the child survived SIGINT: the handler disarmed the default behaviour and never restored it")
	}

	if left := filesIn(t, directory); len(left) != 0 {
		t.Errorf("the interrupt left %v behind", left)
	}
}
