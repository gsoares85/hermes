//go:build !windows

package credential_test

import (
	"os"
	"testing"

	"github.com/gsoares85/hermes/internal/credential"
)

// libpq refuses to read a password file that anyone but its owner can, so this
// is not only our rule: a file with a wider mode is one pg_dump will not use.
func TestThePasswordFileIsReadableOnlyByItsOwner(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	// The directory this package creates, not the one the test framework did:
	// a mode set on the file inside a directory anyone can list is only half
	// a permission.
	store := credential.NewStore(directory + "/credentials")

	handoff, err := store.InFile(target())
	if err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}
	defer func() { _ = handoff.Release() }()

	path := passfileOf(t, handoff)

	file, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the mode of %s: %v", path, err)
	}
	if got := file.Mode().Perm(); got != 0o600 {
		t.Errorf("the mode of %s is %o, want 600", path, got)
	}

	containing, err := os.Stat(directory + "/credentials")
	if err != nil {
		t.Fatalf("reading the mode of the directory: %v", err)
	}
	if got := containing.Mode().Perm(); got != 0o700 {
		t.Errorf("the mode of the directory is %o, want 700", got)
	}
}
