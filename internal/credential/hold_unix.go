//go:build !windows

package credential

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
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
// closed as soon as the content is written. Duplicating the descriptor is not
// an option for the same reason: a dup shares the description, and would share
// its end.
//
// Reopening by name is therefore unavoidable, and it opens a window: between
// the file being created and being reopened, the name could be made to point
// somewhere else. The password does not escape either way — it is written
// through the original descriptor — but PGPASSFILE would name a file of
// somebody else's, and a child would authenticate against records nobody here
// wrote. O_NOFOLLOW refuses to open a symbolic link at all, and the identity
// check refuses everything else: what came back has to be the very file that
// was created, not another one that took its name.
func hold(file *os.File) (io.Closer, error) {
	claim, err := os.OpenFile(file.Name(), os.O_RDWR|syscall.O_NOFOLLOW, fileMode)
	if err != nil {
		return nil, fmt.Errorf("opening %s to claim it: %w", file.Name(), err)
	}

	if err := sameFile(file, claim); err != nil {
		_ = claim.Close()

		return nil, err
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
	if errors.Is(err, os.ErrNotExist) {
		// Already gone: another sweep got there first, which is the outcome
		// asked for and not worth a word.
		return false
	}
	if err != nil {
		// This is not "in use", it is "could not ask" — a mode somebody
		// changed, an access rule, a filesystem that went away. Answered the
		// conservative way like every other doubt here, and said out loud,
		// because a file in this state is never swept: the password stays on
		// the disk for ever and nothing else would ever mention it.
		slog.Warn("could not ask whether a password file is still in use",
			"file", path, "error", err)

		return false
	}
	defer func() { _ = file.Close() }()

	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

// sameFile reports that the reopened name is still the file that was created,
// by the only identity a filesystem has: the device and the inode.
//
// A name is not an identity. Between the file being created and being reopened
// somebody could have replaced it, and a claim taken on the replacement is a
// claim on nothing — the sweep would collect the real file, and the child would
// read whatever the imposter holds.
func sameFile(created, reopened *os.File) error {
	original, err := created.Stat()
	if err != nil {
		return fmt.Errorf("looking at %s: %w", created.Name(), err)
	}

	current, err := reopened.Stat()
	if err != nil {
		return fmt.Errorf("looking at %s: %w", reopened.Name(), err)
	}

	if !os.SameFile(original, current) {
		return fmt.Errorf("claiming %s: it is no longer the file that was created", created.Name())
	}

	return nil
}
