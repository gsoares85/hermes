// Package privatedir creates the directories Hermes keeps a person's files in,
// and makes sure they stay theirs.
//
// It exists because os.MkdirAll only applies a mode to what it creates. A
// directory that is already there keeps whatever mode it has, so a Hermes
// directory left behind by an older build, restored from a backup, or created
// by a script with a wide umask stays wide for ever — and the two callers of
// this both put files in one that nobody else has any business listing.
//
// The files themselves are 0600 either way. What a wide directory gives away is
// the listing: which servers someone connects to, and that a password file
// exists at this moment, which is the moment it is worth attacking.
package privatedir

import (
	"fmt"
	"os"
)

// Mode is what a directory of Hermes is created with, and narrowed to.
const Mode = 0o700

// Make creates the directory and every parent it needs, and narrows it if it
// was already there and wider.
//
// Only narrowing, never widening: a person who deliberately made their own
// configuration directory stricter than this has said something, and a program
// that undid it would be answering a question nobody asked.
func Make(path string) error {
	if err := os.MkdirAll(path, Mode); err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("looking at %s: %w", path, err)
	}

	return narrow(path, info.Mode().Perm())
}
