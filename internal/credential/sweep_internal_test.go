package credential

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The signal path is tested from inside the package because a test cannot send
// a signal to itself portably: Windows has no way to deliver an interrupt to
// the process that asked for it. What is worth proving is what happens once the
// signal has arrived, so the channel it arrives on is handed in.

func waiting() Target {
	return Target{Host: "db.example.com", Port: 5432, Database: "app", User: "reader", Password: "s3cr3t"}
}

func TestAnInterruptRemovesTheFilesOfThisProcess(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store := NewStore(directory)

	if _, err := store.InFile(waiting()); err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}

	signals := make(chan os.Signal, 1)
	finished := make(chan struct{})

	interrupted := make(chan os.Signal, 1)

	go func() {
		interrupted <- store.releaseOnSignal(t.Context(), signals, make(chan struct{}))
		close(finished)
	}()

	signals <- os.Interrupt
	<-finished

	if entries, _ := os.ReadDir(directory); len(entries) != 0 {
		t.Errorf("%d files survived the interrupt", len(entries))
	}
	// Answering the signal is what tells the caller to hand it back to the
	// process. Answering nil here would leave the program unkillable, which is
	// the defect this assertion exists to catch.
	if got := <-interrupted; got != os.Interrupt {
		t.Errorf("releaseOnSignal answered %v, want the signal it consumed", got)
	}
}

// The context is the lifetime of the caller, not a shutdown of the process. It
// ends the watch and nothing else — an operation still running keeps its file.
func TestTheEndOfTheContextRemovesNothing(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store := NewStore(directory)

	if _, err := store.InFile(waiting()); err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan struct{})

	go func() {
		if got := store.releaseOnSignal(ctx, make(chan os.Signal), make(chan struct{})); got != nil {
			t.Errorf("releaseOnSignal answered %v, want nil: no signal arrived", got)
		}
		close(finished)
	}()

	cancel()
	<-finished

	if entries, _ := os.ReadDir(directory); len(entries) != 1 {
		t.Errorf("the directory holds %d files, want the one that is still in use", len(entries))
	}
}

// The same, for the function ReleaseOnInterrupt hands back: it unregisters the
// watch, and a caller that deferred it has not asked for a cleanup.
func TestStoppingTheWatchEndsIt(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store := NewStore(directory)

	if _, err := store.InFile(waiting()); err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}

	stopping := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		if got := store.releaseOnSignal(t.Context(), make(chan os.Signal), stopping); got != nil {
			t.Errorf("releaseOnSignal answered %v, want nil: no signal arrived", got)
		}
		close(finished)
	}()

	close(stopping)
	<-finished

	if entries, _ := os.ReadDir(directory); len(entries) != 1 {
		t.Errorf("the directory holds %d files, want the one that is still in use", len(entries))
	}
}

// A file name that is not one of ours, whatever it looks like, names no process
// and is never touched. The sweep reads this before it reads a directory entry.
func TestOwnerOfReadsOnlyTheNamesThisPackageWrites(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"pgpass-4242-Xy9":      "4242",
		"pgpass-1-":            "1",
		"pgpass-":              "",
		"pgpass-abc-x":         "",
		"pgpass--1-x":          "",
		"pgpass-0-x":           "",
		"connections.toml":     "",
		"almost-pgpass-1-x":    "",
		"pgpass4242-x":         "",
		"pgpass-99999999999x":  "",
		"pgpass-99999999999-x": "",
		// strconv.Atoi accepts both of these and neither is a name this
		// package writes. The claim in the name of this test is "only the
		// names this package writes", so it has to hold for the near misses
		// as well as for the obvious ones.
		"pgpass-+4242-x":  "",
		"pgpass-004242-x": "",
	} {
		got, ours := ownerOf(name)
		if want == "" && ours {
			t.Errorf("ownerOf(%q) claimed the file for process %s", name, got)
		}
		// Both halves of the answer, because the second is the one that
		// decides anything: a name reported as not ours is skipped by Sweep
		// and by ReleaseAll alike. Asserting only the identifier would pass
		// while every file this package writes went unrecognised — a password
		// left on the disk for ever, which is the outcome the package exists
		// to prevent.
		if want != "" && (!ours || got != want) {
			t.Errorf("ownerOf(%q) = %q, %t, want %q, true", name, got, ours, want)
		}
	}
}

// The failure paths of a package that writes secrets to disk are worth as much
// as its happy path: one that reports success while leaving a file behind is
// the bug this whole package exists to avoid.

// A directory cannot be removed while it holds something, which is what a
// handoff whose file has been replaced by one would run into.
func TestReleaseReportsWhatItCouldNotRemove(t *testing.T) {
	t.Parallel()

	occupied := filepath.Join(t.TempDir(), "occupied")
	if err := os.MkdirAll(filepath.Join(occupied, "inside"), 0o700); err != nil {
		t.Fatalf("preparing the directory: %v", err)
	}

	if err := (Handoff{path: occupied}).Release(); err == nil {
		t.Error("Release() = nil for something it could not remove")
	}
}

// Nothing may be written when the directory cannot be made, and the error has
// to name the place rather than the operation.
func TestInFileReportsADirectoryItCannotCreate(t *testing.T) {
	t.Parallel()

	notADirectory := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADirectory, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", notADirectory, err)
	}

	if _, err := NewStore(filepath.Join(notADirectory, "credentials")).InFile(waiting()); err == nil {
		t.Error("InFile(...) = nil for a directory it could not create")
	}
}

// The handoff describes itself in each of its three states, and in none of them
// does it describe the credential.
func TestTheHandoffDescribesWhichRouteItTook(t *testing.T) {
	t.Parallel()

	for name, expected := range map[string]struct {
		handoff Handoff
		says    string
	}{
		"nothing":         {Handoff{}, "no credential"},
		"the environment": {Handoff{env: []string{"PGPASSWORD=s3cr3t"}}, "environment"},
		"a file":          {Handoff{env: []string{"PGPASSFILE=/tmp/x"}, path: "/tmp/x"}, "/tmp/x"},
	} {
		got := expected.handoff.String()
		if !strings.Contains(got, expected.says) {
			t.Errorf("the handoff through %s says %q, want it to mention %q", name, got, expected.says)
		}
		if strings.Contains(got, "s3cr3t") {
			t.Errorf("the handoff through %s says %q, the secret survived", name, got)
		}
	}
}
