package credential

import (
	"io"
	"os"
	"strings"
	"testing"
)

// The order create, claim, write is the whole reason the race window is
// harmless, and nothing asserted it. A refactor moving the claim after the
// content passes every other test in this package and reopens the window the
// sweep was caught in under load: a file with a password in it and nothing
// saying it is in use, which any sweep on the machine is then entitled to take.
//
// What is asserted is the invariant rather than the order of two lines: at the
// moment the claim is taken, the file holds nothing. That stays true through
// any rewriting that keeps the guarantee and fails on any that does not.
func TestTheFileIsClaimedBeforeThereIsAnythingInIt(t *testing.T) {
	t.Parallel()

	store := NewStore(t.TempDir())

	claimed := false
	watching := func(file *os.File) (io.Closer, error) {
		claimed = true

		content, err := os.ReadFile(file.Name())
		if err != nil {
			t.Errorf("reading %s at the moment it was claimed: %v", file.Name(), err)
		}
		if len(content) != 0 {
			t.Errorf("the file already held %d bytes when it was claimed: %q", len(content), content)
		}

		return hold(file)
	}

	path, claim, err := store.write("db.example.com:5432:app:reader:s3cr3t\n", watching)
	if err != nil {
		t.Fatalf("write(...) = %v", err)
	}
	defer func() { _ = claim.Close() }()

	if !claimed {
		t.Fatal("the file was never claimed")
	}

	// And the content did arrive, so this is not passing by writing nothing.
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.Contains(string(written), "s3cr3t") {
		t.Errorf("%s does not hold the record: %q", path, written)
	}
}

// The other half of the same order: a claim that cannot be taken leaves no
// file behind, because there is nothing in it yet to leave.
func TestAFileThatCannotBeClaimedIsNotLeftBehind(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store := NewStore(directory)

	var created string
	refusing := func(file *os.File) (io.Closer, error) {
		created = file.Name()

		return nil, os.ErrPermission
	}

	if _, _, err := store.write("db.example.com:5432:app:reader:s3cr3t\n", refusing); err == nil {
		t.Fatal("write(...) = nil for a file it could not claim")
	}
	if created == "" {
		t.Fatal("no file was created, so nothing was tested")
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Errorf("%s is still there after the claim was refused: %v", created, err)
	}
}
