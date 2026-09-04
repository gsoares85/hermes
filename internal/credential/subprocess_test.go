package credential_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/credential"
)

// The test the whole package is built around: a child process is given a
// password, and the operating system's own view of that child's command line
// does not contain it.
//
// It is asked of both routes, because both are ways of starting a child and
// either could be the one that puts a secret where every process of this user
// can read it.
func TestThePasswordNeverReachesTheCommandLineOfAChild(t *testing.T) {
	t.Parallel()

	routes := map[string]func(*testing.T) credential.Handoff{
		"through the environment": func(t *testing.T) credential.Handoff {
			t.Helper()

			handoff, err := credential.InEnvironment(password)
			if err != nil {
				t.Fatalf("InEnvironment(...) = %v", err)
			}

			return handoff
		},
		"through a password file": func(t *testing.T) credential.Handoff {
			t.Helper()

			handoff, err := credential.NewStore(t.TempDir()).InFile(target())
			if err != nil {
				t.Fatalf("InFile(...) = %v", err)
			}
			t.Cleanup(func() { _ = handoff.Release() })

			return handoff
		},
	}

	for name, prepare := range routes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handoff := prepare(t)

			child := newHelper(t, reportCredential)
			handoff.Apply(child.command)
			child.start(t)

			var seen credentialSeenByChild
			child.reported(t, &seen)

			commandLine := commandLineOf(t, child.command.Process.Pid)

			// A reader that returned nothing would pass the assertion below
			// without ever having looked, which is the failure mode of a test
			// like this one.
			if !strings.Contains(commandLine, "TestHelperProcess") {
				t.Fatalf("the command line read from the system is %q, which is not the child", commandLine)
			}
			if strings.Contains(commandLine, password) {
				t.Errorf("the command line of the child is %q, the password is on it", commandLine)
			}

			// And the handoff has to have worked. A route that hands over
			// nothing would satisfy everything above.
			assertChildCanAuthenticate(t, seen)
		})
	}
}

// assertChildCanAuthenticate checks the child was actually given the password,
// by whichever route it arrived.
func assertChildCanAuthenticate(t *testing.T, seen credentialSeenByChild) {
	t.Helper()

	if seen.Password == password {
		return
	}

	if seen.Passfile == "" {
		t.Fatalf("the child was given neither a password nor a password file: %+v", seen)
	}

	content, err := os.ReadFile(seen.Passfile)
	if err != nil {
		t.Fatalf("reading the password file the child was pointed at: %v", err)
	}
	if !strings.Contains(string(content), password) {
		t.Errorf("the password file the child was pointed at holds %q, want the password in it", content)
	}
}

// The layer that makes the promise true. A process killed outright runs nothing
// deferred, answers no signal and closes nothing: the file it wrote is still
// there, and the only thing that can remove it is the next start-up.
func TestTheSweepRemovesTheFileOfAProcessThatWasKilled(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()

	child := newHelper(t, writePassfile, helperDirectory+"="+directory)
	child.start(t)

	var wrote string
	child.reported(t, &wrote)

	left := filesIn(t, directory)
	if len(left) != 1 {
		t.Fatalf("the child reported %q but left %d files in %s", wrote, len(left), directory)
	}

	// Killed, not asked to stop. This is the case no handler of ours can cover,
	// and the reason the sweep exists at all.
	if err := child.command.Process.Kill(); err != nil {
		t.Fatalf("killing the child: %v", err)
	}
	if err := child.command.Wait(); err == nil {
		t.Log("the child reported a clean exit after being killed")
	}

	if still := filesIn(t, directory); len(still) != 1 {
		t.Fatalf("the password file went before the sweep ran: %v", still)
	}

	removed, err := credential.NewStore(directory).Sweep()
	if err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	if removed != 1 {
		t.Errorf("Sweep() removed %d files, want the one the killed process left", removed)
	}
	if still := filesIn(t, directory); len(still) != 0 {
		t.Errorf("the sweep left %v behind", still)
	}
}

// A second Hermes starting up must not delete the file the first one is in the
// middle of an operation with. This process is alive by definition, so its own
// files are the ones a sweep has to leave alone.
func TestTheSweepLeavesTheFileOfALiveProcessAlone(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store := credential.NewStore(directory)

	handoff, err := store.InFile(target())
	if err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}
	defer func() { _ = handoff.Release() }()

	removed, err := store.Sweep()
	if err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	if removed != 0 {
		t.Errorf("Sweep() removed %d files of a running process", removed)
	}
	if left := filesIn(t, directory); len(left) != 1 {
		t.Errorf("the directory holds %v, want the file of this process still there", left)
	}
}

// Everything else in the directory belongs to somebody else. A sweep that
// cleared what it did not recognise would be a program that deletes files it
// was never asked about.
func TestTheSweepTouchesNothingItDidNotWrite(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()

	strangers := []string{"notes.txt", "pgpass", "pgpass-", "pgpass-notanumber-x", "pgpass--1-x"}
	for _, name := range strangers {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("not ours"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	removed, err := credential.NewStore(directory).Sweep()
	if err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	if removed != 0 {
		t.Errorf("Sweep() removed %d files it did not write", removed)
	}
	if left := filesIn(t, directory); len(left) != len(strangers) {
		t.Errorf("the directory holds %v, want all %d files it started with", left, len(strangers))
	}
}

// On a first run nobody has written a password file yet, and greeting that with
// a failure would report a fault for having done nothing wrong.
func TestTheSweepOfADirectoryThatIsNotThereIsNotAFailure(t *testing.T) {
	t.Parallel()

	removed, err := credential.NewStore(filepath.Join(t.TempDir(), "never-created")).Sweep()
	if err != nil {
		t.Errorf("Sweep() = %v, want nil", err)
	}
	if removed != 0 {
		t.Errorf("Sweep() removed %d files from a directory that does not exist", removed)
	}
}

// ReleaseAll is the counterpart: it takes the files of this process, which are
// exactly the ones the sweep will not touch while it is running.
func TestReleaseAllRemovesWhatThisProcessWrote(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store := credential.NewStore(directory)

	if _, err := store.InFile(target()); err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}
	if _, err := store.InFile(target()); err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}

	stranger := filepath.Join(directory, "notes.txt")
	if err := os.WriteFile(stranger, []byte("not ours"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", stranger, err)
	}

	removed, err := store.ReleaseAll()
	if err != nil {
		t.Fatalf("ReleaseAll() = %v", err)
	}
	if removed != 2 {
		t.Errorf("ReleaseAll() removed %d files, want the two this process wrote", removed)
	}

	if left := filesIn(t, directory); len(left) != 1 || left[0] != "notes.txt" {
		t.Errorf("the directory holds %v, want only the file that is not ours", left)
	}
}

// ReleaseOnInterrupt must stop watching without taking anything with it: a
// caller that has finished waiting has not finished the operation.
func TestStoppingTheWatchRemovesNothing(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store := credential.NewStore(directory)

	handoff, err := store.InFile(target())
	if err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}
	defer func() { _ = handoff.Release() }()

	stop := store.ReleaseOnInterrupt(t.Context())
	stop()
	// Twice, because it is deferred by a caller that may also call it early.
	stop()

	if left := filesIn(t, directory); len(left) != 1 {
		t.Errorf("the directory holds %v, want the file still there", left)
	}
}

func filesIn(t *testing.T, directory string) []string {
	t.Helper()

	entries, err := os.ReadDir(directory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reading %s: %v", directory, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

// Guard is what a binary calls at the start: it sweeps what an earlier run left
// behind, and its release takes the files of this run with it.
func TestGuardSweepsAtTheStartAndReleasesAtTheEnd(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()

	// A file an earlier run left behind, named for a process that has gone.
	stale := filepath.Join(directory, "pgpass-"+strconv.Itoa(deadProcess(t))+"-earlier")
	if err := os.WriteFile(stale, []byte("db:5432:app:reader:s3cr3t\n"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", stale, err)
	}

	store := credential.NewStore(directory)
	release := store.Guard(t.Context())

	ours, err := store.InFile(target())
	if err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}

	release()

	if left := filesIn(t, directory); len(left) != 0 {
		t.Errorf("the directory holds %v, want the stale file swept and ours released", left)
	}
	// The handoff of this run knows its file has gone, and says so without
	// complaining about it.
	if err := ours.Release(); err != nil {
		t.Errorf("Release() after the guard released it = %v, want nil", err)
	}
}

// deadProcess returns the identifier of a process that has certainly finished:
// one this test started and waited for.
func deadProcess(t *testing.T) int {
	t.Helper()

	child := newHelper(t, reportCredential)
	child.start(t)

	var seen credentialSeenByChild
	child.reported(t, &seen)

	pid := child.command.Process.Pid
	if err := child.command.Process.Kill(); err != nil {
		t.Fatalf("killing the child: %v", err)
	}
	_ = child.command.Wait()

	return pid
}
