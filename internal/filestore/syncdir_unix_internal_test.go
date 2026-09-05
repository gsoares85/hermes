//go:build !windows

package filestore

import (
	"path/filepath"
	"testing"
)

// Only where the flush is real. A save that cannot flush the directory it just
// renamed into has not finished, and has to say so rather than report a
// durability it did not get.
func TestFlushingADirectoryThatIsNotThereFails(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "gone")
	if err := syncDir(missing); err == nil {
		t.Errorf("syncDir(%q) = nil, want an error: the directory is not there", missing)
	}
}
