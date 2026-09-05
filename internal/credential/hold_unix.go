//go:build !windows

package credential

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

// hold takes the claim that says this file is in use, and answers what keeps
// it. Closing that gives the claim up; the file it was given is the caller's to
// close as usual.
//
// An advisory lock, and the operating system is what ends it: on a normal exit,
// on a crash, on a kill that runs no code of ours, and on the machine losing
// power. That is the whole reason the claim is a lock rather than a note in the
// file name — nothing has to be true about the process for the claim to end
// when the process does.
//
// A descriptor of its own, because a flock lives and dies with the open file
// description it was taken on, and the one the content is written through is
// closed as soon as the content is written.
func hold(file *os.File) (io.Closer, error) {
	claim, err := os.OpenFile(file.Name(), os.O_RDWR, fileMode)
	if err != nil {
		return nil, fmt.Errorf("opening %s to claim it: %w", file.Name(), err)
	}

	if err := syscall.Flock(int(claim.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = claim.Close()

		return nil, fmt.Errorf("claiming %s: %w", file.Name(), err)
	}

	return closeOnce(claim), nil
}

// orphaned reports whether nothing holds the file any more, which is the only
// question the sweep needs answered: a claim that can be taken is a claim
// nobody is making.
//
// Two open file descriptions conflict even inside one process, so this answers
// "in use" for a file this very process is holding — which is what stops a
// second Hermes, and this one, sweeping a file an operation is still using.
func orphaned(path string) bool {
	file, err := os.OpenFile(path, os.O_RDWR, fileMode)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()

	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}
