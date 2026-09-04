//go:build !windows

package credential

import (
	"os"
	"path/filepath"
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
