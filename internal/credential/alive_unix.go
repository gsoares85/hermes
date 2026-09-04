//go:build !windows

package credential

import (
	"errors"
	"syscall"
)

// alive reports whether a process with this identifier is still running.
//
// Signal zero is the portable way to ask: it performs the permission check and
// the lookup, and delivers nothing. A process owned by someone else answers
// EPERM, which is still an answer that it exists — and while the files this is
// asked about are in a directory of our own user, treating "not allowed to ask"
// as "gone" is the wrong way to be wrong: it would delete a file still in use.
func alive(pid int) bool {
	// Zero is not a process on Unix, it is every process in this group, and
	// asking about it would answer that a file belonging to nobody is in use.
	// Negative identifiers address a group too.
	if pid <= 0 {
		return false
	}

	err := syscall.Kill(pid, 0)

	return err == nil || errors.Is(err, syscall.EPERM)
}
