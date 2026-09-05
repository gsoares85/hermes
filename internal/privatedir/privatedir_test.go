package privatedir_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gsoares85/hermes/internal/privatedir"
)

// A directory that does not exist yet is created private, parents and all.
func TestMakeCreatesTheDirectoryPrivate(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hermes", "credentials")
	if err := privatedir.Make(path); err != nil {
		t.Fatalf("Make(%q) = %v", path, err)
	}

	assertMode(t, path, privatedir.Mode)
}

// The case os.MkdirAll answers wrongly, and the reason this package exists: a
// mode applies to what MkdirAll creates and to nothing that was already there.
// A Hermes directory left behind by an older build, restored from a backup, or
// made by a script with a wide umask stayed wide for ever.
func TestMakeNarrowsADirectoryThatWasAlreadyWide(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "wide")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}

	if err := privatedir.Make(path); err != nil {
		t.Fatalf("Make(%q) = %v", path, err)
	}

	assertMode(t, path, privatedir.Mode)
}

// Narrowing only. Somebody who made their own directory stricter than this has
// said something, and a program that undid it would be answering a question
// nobody asked.
func TestMakeLeavesADirectoryThatIsStricterAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissions there are an access control list, not a mode")
	}

	path := filepath.Join(t.TempDir(), "strict")
	if err := os.MkdirAll(path, 0o500); err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}

	if err := privatedir.Make(path); err != nil {
		t.Fatalf("Make(%q) = %v", path, err)
	}

	assertMode(t, path, 0o500)
}

// A path that cannot be a directory is a failure to report, not one to work
// around.
func TestMakeReportsAPathThatCannotBeADirectory(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", file, err)
	}

	if err := privatedir.Make(filepath.Join(file, "under")); err == nil {
		t.Error("Make(...) = nil for a path under a file")
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	if runtime.GOOS == "windows" {
		// Windows has no mode to read back. What decides who may look inside
		// is the access control list the directory inherits, which the tests
		// of the stores themselves assert by SID.
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the directory is not there: %v", err)
		}

		return
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("looking at %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s is %04o, want %04o", path, got, want)
	}
}
