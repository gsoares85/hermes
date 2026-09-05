//go:build !windows

package privatedir

import (
	"fmt"
	"io/fs"
	"os"
)

// narrow takes away every permission Mode does not grant, and grants nothing.
//
// The mask is what makes that true in one operation: a directory at 0755 comes
// back 0700, and one at 0500 stays 0500 rather than being opened up to 0700.
func narrow(path string, current fs.FileMode) error {
	wanted := current & Mode
	if wanted == current {
		return nil
	}

	if err := os.Chmod(path, wanted); err != nil {
		return fmt.Errorf("narrowing the permissions of %s: %w", path, err)
	}

	return nil
}
