package proc

import (
	"errors"
	"os"
	"runtime"
	"testing"
	"time"
)

// The group is written down when the process starts, not looked up when it is
// killed. By the time somebody cancels, the process may already have been
// waited for, and the identifier it had belongs to whoever the kernel gave it
// to next — signalling that group would reach a stranger.
func TestTheGroupIsWrittenDownWhenTheProcessStarts(t *testing.T) {
	t.Parallel()

	command := sleeping(t)
	if err := command.Start(); err != nil {
		t.Fatalf("Start() = %v, want no error", err)
	}
	t.Cleanup(func() { _ = command.Kill() })

	if command.group == 0 {
		t.Fatal("the group is zero after starting, so killing has nothing to name")
	}

	if runtime.GOOS != "windows" {
		// On Unix the group is the process that leads it, and Setpgid makes
		// that the child itself.
		if command.group != uintptr(command.cmd.Process.Pid) {
			t.Errorf("the group is %d, want the identifier of the process (%d)",
				command.group, command.cmd.Process.Pid)
		}
	}
}

// Killing a process that has already been waited for sends nothing. Its
// identifier is the kernel's again, and a signal to it lands on whoever holds
// it now — which on a machine that has wrapped around its identifiers is
// somebody else's process group, possibly this session's.
func TestKillingAfterWaitingSendsNothing(t *testing.T) {
	t.Parallel()

	// A run of this binary that matches no test at all: it starts, finds
	// nothing to do and exits, which is all this case needs from it.
	command := New(os.Args[0], "-test.run=TestNothingMatchesThisName")

	if err := command.Start(); err != nil {
		t.Fatalf("Start() = %v, want no error", err)
	}

	_ = command.Wait()

	if !command.finished {
		t.Fatal("the command does not know it was collected, so Kill will still signal its identifier")
	}

	if err := command.Kill(); err != nil {
		t.Errorf("Kill() after Wait() = %v, want no error and no signal", err)
	}
}

// A process that started but could not be held is not a started process. On
// Windows the job object is what makes the children die with it, and a
// machine whose policy refuses to open a process fails there — with the
// process already running.
//
// Reporting the failure and keeping it would be the worst of both: the caller
// believes nothing began, so it never waits, and the Kill it might send lands
// on a group that was never formed and answers success having killed nothing.
func TestAProcessThatCannotBeHeldIsNotLeftRunning(t *testing.T) {
	t.Parallel()

	refused := errors.New("this machine will not let a process be held")

	command := sleeping(t)

	var pid int
	err := command.start(func() error {
		pid = command.cmd.Process.Pid

		return refused
	})

	if !errors.Is(err, refused) {
		t.Fatalf("start(...) = %v, want the failure that came back from adopting", err)
	}
	if command.started {
		t.Error("the command reports itself started after it could not be held")
	}

	// Started and taken back: the caller is told nothing began, and nothing is.
	if err := command.Kill(); !errors.Is(err, ErrNotStarted) {
		t.Errorf("Kill() = %v, want ErrNotStarted: a start that failed left something behind", err)
	}
	if err := command.Wait(); !errors.Is(err, ErrNotStarted) {
		t.Errorf("Wait() = %v, want ErrNotStarted", err)
	}

	waitUntilGone(t, pid)
}

// The environment that turns the case below from a skipped test into the
// long-lived process these cases need.
const sleeperVariable = "HERMES_PROC_INTERNAL_SLEEPER"

// TestSleepsUntilKilled is not a test of anything. It is the process the cases
// above start: one that stays alive until somebody ends it, on every system,
// without a fixture to build. It does nothing at all unless this binary was
// re-executed on purpose to be it.
func TestSleepsUntilKilled(t *testing.T) {
	if os.Getenv(sleeperVariable) == "" {
		t.Skip("this is the long-lived process the cases here start, not a test of anything")
	}

	time.Sleep(2 * time.Minute)
}

// sleeping is that process, ready to start.
func sleeping(t *testing.T) *Command {
	t.Helper()

	command := New(os.Args[0], "-test.run=TestSleepsUntilKilled", "-test.timeout=0")
	command.Cmd().Env = append(os.Environ(), sleeperVariable+"=1")

	return command
}

// waitUntilGone fails unless the process stops existing. How that is asked
// differs by system and is answered by running, next to this file.
func waitUntilGone(t *testing.T, pid int) {
	t.Helper()

	if pid == 0 {
		t.Fatal("the process was never started, so there is nothing to look for")
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !running(pid) {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("process %d is still running after a start that failed", pid)
}

// Whatever the operating system needed held while the process was alive is let
// go when it has been collected. On Windows that is the handle of the job
// object: held open it is what makes the tree die together, and kept past the
// end it is a handle of the kernel's that this process never gives back — one
// per backup, for the life of the application.
func TestWhatWasHeldIsLetGoWhenTheProcessEnds(t *testing.T) {
	t.Parallel()

	command := sleeping(t)
	if err := command.Start(); err != nil {
		t.Fatalf("Start() = %v, want no error", err)
	}

	if runtime.GOOS == "windows" && command.group == 0 {
		t.Fatal("nothing is held while the process is alive, so the tree is not held together")
	}

	if err := command.Kill(); err != nil {
		t.Fatalf("Kill() = %v, want no error", err)
	}

	_ = command.Wait()

	if runtime.GOOS == "windows" && command.group != 0 {
		t.Error("the job object is still held after the process was collected")
	}
}

// Two callers arrive at the same process: the goroutine that waits for it, and
// the cancellation that wants to know it is over. Neither is a mistake, and
// os/exec complains at the second — so the wait happens once and both are told
// what it found.
func TestWaitingTwiceAnswersWhatTheFirstWaitFound(t *testing.T) {
	t.Parallel()

	command := New(os.Args[0], "-test.run=TestNothingMatchesThisName")
	if err := command.Start(); err != nil {
		t.Fatalf("Start() = %v, want no error", err)
	}

	first := command.Wait()
	second := command.Wait()

	if first != nil {
		t.Fatalf("Wait() = %v, want no error for a process that ended cleanly", first)
	}
	if second != nil {
		t.Errorf("waiting again answered %v, want what the first wait answered", second)
	}
}

// The command is handed out before it starts, so that a password can go in its
// environment and its output into a log. What a caller sets on it has to
// survive the starting, including the corner of it this package also writes
// to.
func TestWhatTheCallerAsksForOnTheProcessSurvives(t *testing.T) {
	t.Parallel()

	command := sleeping(t)
	command.Cmd().SysProcAttr = attrWithSomethingElse()

	// Prepared rather than started, because what a caller legitimately sets
	// here is mostly about terminals and sessions, and a machine running this
	// in a pipeline has no terminal to detach from: starting would fail for
	// the fixture rather than for the thing being tested. What is asked is
	// whether preparing keeps what it was given, and preparing is where the
	// answer is.
	command.prepare()

	if !somethingElseSurvived(command.Cmd().SysProcAttr) {
		t.Error("what the caller set on the process was written over by preparing it")
	}
}
