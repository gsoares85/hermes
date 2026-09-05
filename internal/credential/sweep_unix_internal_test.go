//go:build !windows

package credential

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Unix only, because the failure has to be distinguishable from the ordinary
// one. Windows answers "the path was not found" when asked to list a file,
// which is the same answer it gives for the directory of a first run — and a
// first run is nothing to sweep, not a fault.
// A store pointed at a file rather than a directory cannot be read, and the
// sweep has to say so instead of reporting that it found nothing to do.
func TestASweepThatCannotReadReportsIt(t *testing.T) {
	t.Parallel()

	notADirectory := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADirectory, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", notADirectory, err)
	}

	removed, err := NewStore(notADirectory).Sweep()
	if err == nil {
		t.Error("Sweep() = nil for a store that could not be read")
	}
	if removed != 0 {
		t.Errorf("Sweep() removed %d files it could not have listed", removed)
	}
}

// The conservative answer to "could not ask" is "in use", which is the right
// direction to err and the wrong direction to stay silent in: a file nothing
// can open is never swept, so the password stays on the disk for ever. The
// warning is the only thing that would ever say so.
//
// Unix only, and skipped for root, because root can open anything.
func TestASweepThatCannotAskAboutAFileSaysSo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can open a file whatever its mode")
	}

	directory := t.TempDir()
	store := NewStore(directory)

	handoff, err := store.InFile(waiting())
	if err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}
	path := handoff.path
	if err := handoff.Release(); err != nil {
		t.Fatalf("Release() = %v", err)
	}

	// Written back with a mode nothing can open, which is the state an access
	// rule somebody changed leaves behind.
	if err := os.WriteFile(path, []byte("h:5432:d:u:s3cr3t\n"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}

	var written bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&written, nil)))
	defer slog.SetDefault(previous)

	removed, err := store.Sweep()
	if err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	if removed != 0 {
		t.Errorf("Sweep() removed %d files, want 0: it could not ask about this one", removed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the file it could not ask about is gone: %v", err)
	}
	if !strings.Contains(written.String(), "could not ask") {
		t.Errorf("the sweep said nothing about the file it could not ask about: %s", written.String())
	}
}
