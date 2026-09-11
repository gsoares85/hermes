package proc_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/proc"
)

// The test subprocess, and the grandchild it starts.
//
// A helper binary rather than a shell script, because the thing being proved
// is the same on three systems and a script is not: sh is not on Windows, and
// what `cmd` does with a background process is not what a shell does with one.
// TestMain re-executes this very test binary with a marker in its environment,
// which is how a test gets a real process tree without a fixture to build.
const (
	roleVariable = "HERMES_PROC_TEST_ROLE"
	roleParent   = "parent"
	roleChild    = "child"

	// Where the grandchild says it is alive. The parent writes nothing: what
	// is being proved is that killing the parent's group reaches the child,
	// and a parent that also wrote would make an empty file ambiguous.
	markerVariable = "HERMES_PROC_TEST_MARKER"
)

func TestMain(m *testing.M) {
	switch os.Getenv(roleVariable) {
	case roleParent:
		parent()
	case roleChild:
		child()
	default:
		os.Exit(m.Run())
	}
}

// parent starts a grandchild and then waits for ever, like pg_restore -j with
// its workers.
func parent() {
	command := exec.Command(os.Args[0])
	command.Env = append(os.Environ(), roleVariable+"="+roleChild)

	if err := command.Start(); err != nil {
		os.Exit(1)
	}

	select {}
}

// child touches its marker every so often, for as long as it is alive. A file
// whose modification time stops advancing is a process that stopped running,
// which is what the test measures.
func child() {
	marker := os.Getenv(markerVariable)

	for {
		if err := os.WriteFile(marker, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(1)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

// startedTree starts the parent, waits until the grandchild has proved it is
// running, and answers the command and the marker.
func startedTree(t *testing.T) (*proc.Command, string) {
	t.Helper()

	marker := filepath.Join(t.TempDir(), "alive")

	command := proc.New(os.Args[0])
	command.Cmd().Env = append(os.Environ(),
		roleVariable+"="+roleParent,
		markerVariable+"="+marker,
	)

	if err := command.Start(); err != nil {
		t.Fatalf("Start() = %v, want no error", err)
	}

	t.Cleanup(func() {
		_ = command.Kill()
	})

	waitFor(t, "the grandchild to start", func() bool {
		_, err := os.Stat(marker)

		return err == nil
	})

	return command, marker
}

// waitFor polls until something is true, or fails the test. Polling rather
// than sleeping a fixed time: a process tree takes as long as the machine
// takes, and a fixed sleep is either slow or flaky.
func waitFor(t *testing.T, what string, until func() bool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if until() {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", what)
}

// The criterion this package exists for.
//
// pg_restore -j starts workers. Killing the parent leaves them orphaned and
// still writing to the database after the person cancelled, which is the
// failure nobody sees until a restore somebody stopped has half finished.
func TestKillingReachesTheGrandchild(t *testing.T) {
	t.Parallel()

	command, marker := startedTree(t)

	if err := command.Kill(); err != nil {
		t.Fatalf("Kill() = %v, want no error", err)
	}

	// The grandchild writes every 20ms. If it is dead, the file stops
	// changing; if it is alive, it does not. Two readings far enough apart
	// tell the two cases apart without asking the operating system about a
	// process identifier that may already have been given to somebody else.
	waitFor(t, "the grandchild to stop writing", func() bool {
		return stopped(t, marker)
	})
}

// stopped reports whether the marker has stopped being written.
func stopped(t *testing.T, marker string) bool {
	t.Helper()

	before, err := os.Stat(marker)
	if err != nil {
		return false
	}

	time.Sleep(200 * time.Millisecond)

	after, err := os.Stat(marker)
	if err != nil {
		return false
	}

	return after.ModTime().Equal(before.ModTime())
}

func TestKillingEndsTheProcessItStarted(t *testing.T) {
	t.Parallel()

	command, _ := startedTree(t)

	if err := command.Kill(); err != nil {
		t.Fatalf("Kill() = %v, want no error", err)
	}

	// Wait answers once the process is gone. That it answers at all is the
	// proof; what it answers is an operating system's way of saying "killed",
	// and the three do not agree on the words.
	if err := command.Wait(); err == nil {
		t.Error("Wait() = nil after a kill, want the error of a killed process")
	}
}

func TestWaitingForAProcessThatEndsOnItsOwn(t *testing.T) {
	t.Parallel()

	// No role in the environment, and a -test.run pattern that matches
	// nothing: the binary starts, runs no test and exits cleanly, which is the
	// shortest well-behaved process this test has to hand.
	command := proc.New(os.Args[0], "-test.run=XXXNOTHINGXXX")

	if err := command.Start(); err != nil {
		t.Fatalf("Start() = %v, want no error", err)
	}

	if err := command.Wait(); err != nil {
		t.Errorf("Wait() = %v, want no error", err)
	}
}

// Cancelling twice is one cancellation here as much as in the queue above, and
// the second kill lands on a process that is already gone.
func TestKillingTwice(t *testing.T) {
	t.Parallel()

	command, _ := startedTree(t)

	if err := command.Kill(); err != nil {
		t.Fatalf("Kill() = %v, want no error", err)
	}

	if err := command.Wait(); err == nil {
		t.Error("Wait() = nil after a kill, want an error")
	}

	if err := command.Kill(); err != nil {
		t.Errorf("the second Kill() = %v, want no error", err)
	}
}

// Killing something that was never started is a mistake in the caller, and a
// silent success would hide it.
func TestKillingSomethingThatNeverStarted(t *testing.T) {
	t.Parallel()

	command := proc.New(os.Args[0])

	if err := command.Kill(); !errors.Is(err, proc.ErrNotStarted) {
		t.Errorf("Kill() = %v, want %v", err, proc.ErrNotStarted)
	}

	if err := command.Wait(); !errors.Is(err, proc.ErrNotStarted) {
		t.Errorf("Wait() = %v, want %v", err, proc.ErrNotStarted)
	}
}

func TestStartingSomethingThatIsNotThere(t *testing.T) {
	t.Parallel()

	command := proc.New(filepath.Join(t.TempDir(), "no-such-binary"))

	err := command.Start()
	if err == nil {
		t.Fatal("Start() = nil, want an error")
	}

	// The name has to be in the message. "fork/exec: no such file" says
	// nothing about which binary Hermes went looking for, and the answer to
	// that is what the person has to act on.
	if !strings.Contains(err.Error(), "no-such-binary") {
		t.Errorf("Start() = %v, want it to name the binary", err)
	}
}

// What the caller attaches before the process starts has to survive into it.
// It is how a credential reaches the environment and how the log reaches the
// output, and both go through this seam.
func TestWhatIsAttachedBeforeStartingSurvives(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "alive")

	command := proc.New(os.Args[0])
	command.Cmd().Env = append(os.Environ(),
		roleVariable+"="+roleChild,
		markerVariable+"="+marker,
	)

	if err := command.Start(); err != nil {
		t.Fatalf("Start() = %v, want no error", err)
	}

	t.Cleanup(func() {
		_ = command.Kill()
	})

	waitFor(t, "the child to write its marker", func() bool {
		_, err := os.Stat(marker)

		return err == nil
	})

	kept, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("reading the marker: %v", err)
	}

	if _, err := strconv.Atoi(strings.TrimSpace(string(kept))); err != nil {
		t.Errorf("the marker holds %q, want the child's process identifier", kept)
	}
}
