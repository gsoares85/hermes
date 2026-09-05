//go:build !windows

package filestore

import (
	"fmt"
	"os"
)

// syncDir flushes the directory entry a rename has just created.
//
// It is the other half of the durability the file's own Sync starts, and it is
// the half that was missing. In POSIX a rename is atomic with respect to other
// processes, and that is all it is: the entry it writes lives in the directory,
// and nothing says the directory has reached the disk. A power cut in that
// window loses the rename and brings the old file back — the bytes were safe
// and the name still pointed at the previous ones.
//
// The directory is opened read-only, which is what fsync on a directory takes
// on Linux and on macOS.
func syncDir(path string) error {
	// The path is the directory of the connections file, which this package
	// chose and has just written into.
	dir, err := os.Open(path) //nolint:gosec // the directory we were configured with
	if err != nil {
		return fmt.Errorf("opening %s to flush it: %w", path, err)
	}
	defer func() { _ = dir.Close() }()

	if err := dir.Sync(); err != nil {
		return fmt.Errorf("flushing %s to the disk: %w", path, err)
	}

	return nil
}
