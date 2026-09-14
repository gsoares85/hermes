//go:build !windows

package credential

import (
	"errors"
	"os"
	"os/exec"
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

// The variable that turns this test binary into a process that hands back a
// signal nothing dies from, and the signal it hands back. Go's runtime ignores
// SIGCHLD when nobody is listening for it, exactly as the kernel would.
const harmlessMode = "HERMES_CREDENTIAL_RERAISE_HARMLESS"

const harmlessSignal = syscall.SIGCHLD

// TestHelperReraiseHarmless is not a test. It is the body of the child process
// the case below starts, and it does nothing at all when run on its own.
func TestHelperReraiseHarmless(t *testing.T) {
	if os.Getenv(harmlessMode) == "" {
		t.Skip("this is the body of a child process, not a test")
	}

	reraise(harmlessSignal)

	t.Fatal("reraise returned: the one outcome it exists to prevent")
}

// reraise must end the process even when the signal it handed back does not.
//
// A process is more than the thread that sends the signal: kill(2) makes the
// signal pending and returns, and the kernel delivers it to whichever thread
// has it unblocked — in a Go program, rarely the one that called. Returning as
// soon as the send succeeds is therefore racing the death, and the caller is a
// goroutine in a process whose cleanup has already run: losing that race leaves
// the program alive with its handler already unregistered, which is the one
// state it must never be in.
//
// A signal nothing dies from turns that race into a certainty, and asks the
// question the interrupt case can only ask when the timing goes badly.
func TestReraiseEndsTheProcessEvenWhenTheSignalDoesNot(t *testing.T) {
	t.Parallel()

	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHelperReraiseHarmless$")
	command.Env = append(os.Environ(), harmlessMode+"=1")

	err := command.Run()
	if err == nil {
		t.Fatal("the child exited cleanly: reraise let it live, or ended it as a success")
	}

	var exited *exec.ExitError
	if !errors.As(err, &exited) {
		t.Fatalf("the child failed for another reason: %v", err)
	}

	if got, want := exited.ExitCode(), statusAfter(harmlessSignal); got != want {
		t.Errorf("the child exited with %d, want %d", got, want)
	}
}
