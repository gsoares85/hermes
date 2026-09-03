//go:build linux

package vault

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// A Linux without a keyring is the case ADR-0010 had to decide, and it is the
// one that has to be proven rather than described: a machine with no Secret
// Service gets the in-memory vault, a warning naming what to install, and — the
// part that is the first acceptance criterion of this work — not one byte
// written anywhere.
//
// The bus address points at a socket that does not exist, which is what a
// container, a server session and an ssh login without a desktop all look like
// from here.
func TestWithoutASecretServiceTheVaultFallsBackToMemory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "run"))
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(home, "there-is-no-bus-here"))

	vault, status := Open(t.Context())
	t.Cleanup(func() { _ = vault.Close() })

	if status.Backend != BackendMemory {
		t.Errorf("backend is %q, want %q", status.Backend, BackendMemory)
	}
	if status.Warning == "" {
		t.Error("a session with no keyring got no warning: the failure is silent")
	}

	// Which reference is written under does not matter; that a write happened
	// at all does, because the assertion below is about what it left behind.
	if err := vault.Set(t.Context(), probe, "hunter2"); err != nil {
		t.Fatalf("storing a secret in the fallback vault: %v", err)
	}

	if left := filesUnder(t, home); len(left) != 0 {
		t.Errorf("the fallback wrote %v: no secret of this application reaches a disk", left)
	}
}

func filesUnder(t *testing.T, root string) []string {
	t.Helper()

	var found []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			found = append(found, path)
		}

		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walking %s: %v", root, err)
	}

	return found
}
