package credential

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

// The variable that turns this test binary into a process whose only job is to
// hand a signal back. reraise ends the process, so it cannot be called in a
// test that wants to report anything afterwards.
const reraiseMode = "HERMES_CREDENTIAL_RERAISE"

// TestHelperReraise is not a test. It is the body of the child process the test
// below starts, and it does nothing at all when run on its own.
func TestHelperReraise(t *testing.T) {
	if os.Getenv(reraiseMode) == "" {
		t.Skip("this is the body of a child process, not a test")
	}

	reraise(os.Interrupt)

	t.Fatal("reraise returned: the one outcome it exists to prevent")
}

// The assertion interrupt_unix_test.go promised and nobody wrote. Its comment
// said the Windows guarantee "is asserted by the exit that reraise performs",
// and nothing asserted it — reraise_windows.go had no test of any kind, so the
// half of the promise that platform keeps was a sentence in a comment.
//
// This runs everywhere, because the promise is the same everywhere and only the
// mechanism differs: on Unix the process is killed by the signal it was given,
// so a shell reports 128+n and a supervisor sees a termination; on Windows,
// where a process cannot signal itself, it exits with the number a shell would
// have reported.
func TestReraiseEndsTheProcess(t *testing.T) {
	t.Parallel()

	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHelperReraise$")
	command.Env = append(os.Environ(), reraiseMode+"=1")

	err := command.Run()
	if err == nil {
		t.Fatal("the child exited cleanly: reraise let it live, or ended it as a success")
	}

	var exited *exec.ExitError
	if !errors.As(err, &exited) {
		t.Fatalf("the child failed for another reason: %v", err)
	}

	assertInterrupted(t, exited.ProcessState)
}
