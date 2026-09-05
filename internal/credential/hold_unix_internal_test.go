//go:build !windows

package credential

import (
	"os"
	"path/filepath"
	"testing"
)

// The claim has to be reopened by name — a duplicated descriptor would share
// the open file description, and therefore share the end of the one the content
// is written through. What the name refers to can change in between, and these
// are the two ways it can.
//
// Neither leaks the password: it is written through the descriptor the file was
// created on, wherever the name has gone. What it would cost is PGPASSFILE
// naming somebody else's file, and a child authenticating against records
// nobody here wrote.
func TestAClaimRefusesAnythingButTheFileThatWasCreated(t *testing.T) {
	t.Parallel()

	cases := map[string]func(t *testing.T, dir, path string){
		"a symbolic link put in its place": func(t *testing.T, dir, path string) {
			t.Helper()

			elsewhere := filepath.Join(dir, "elsewhere")
			if err := os.WriteFile(elsewhere, []byte("theirs"), 0o600); err != nil {
				t.Fatalf("writing %s: %v", elsewhere, err)
			}
			if err := os.Symlink(elsewhere, path); err != nil {
				t.Fatalf("linking %s: %v", path, err)
			}
		},
		"another file of the same name": func(t *testing.T, dir, path string) {
			t.Helper()

			if err := os.WriteFile(path, []byte("theirs"), 0o600); err != nil {
				t.Fatalf("writing %s: %v", path, err)
			}
		},
	}

	for name, swap := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "pgpass-1-abc")

			file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, fileMode)
			if err != nil {
				t.Fatalf("creating %s: %v", path, err)
			}
			defer func() { _ = file.Close() }()

			if err := os.Remove(path); err != nil {
				t.Fatalf("removing %s: %v", path, err)
			}
			swap(t, dir, path)

			claim, err := hold(file)
			if err == nil {
				_ = claim.Close()

				t.Fatal("hold(...) claimed a file that is not the one it was given")
			}
		})
	}
}

// The ordinary case still works, which is the half a refusal this strict is
// most likely to break.
func TestAClaimIsTakenOnTheFileThatWasCreated(t *testing.T) {
	t.Parallel()

	file, err := os.CreateTemp(t.TempDir(), "pgpass-1-*")
	if err != nil {
		t.Fatalf("CreateTemp(...) = %v", err)
	}
	defer func() { _ = file.Close() }()

	claim, err := hold(file)
	if err != nil {
		t.Fatalf("hold(...) = %v", err)
	}
	if err := claim.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
}
