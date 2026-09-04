// Package filestore is the outside of the files Hermes keeps for a person.
//
// internal/core/profile decides what the bytes of the connections file say;
// this decides where they go, who may read them, and what happens when the
// power cuts halfway through a write. Splitting it this way is what lets every
// decision about the format be tested against bytes instead of against a
// filesystem, and it is why the interface this satisfies is declared by
// internal/ui, which consumes it, rather than here.
package filestore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/core/profile"
)

// The modes files and directories of Hermes are created with.
//
// The connections file holds no password — the format has no field for one —
// but it does hold every host someone can reach and the user they reach it as,
// which is a map of where to attack and who to go in as. It is theirs to read.
const (
	fileMode = 0o600
	dirMode  = 0o700
)

// Connections is the connections file on disk.
type Connections struct {
	path string
}

// NewConnections builds the store for a file at a path. The path is given
// rather than found so that a test can point it somewhere harmless, and so that
// a person can eventually be allowed to point it somewhere of their own.
func NewConnections(path string) *Connections {
	return &Connections{path: path}
}

// ConnectionsPath is where the file lives when nobody has said otherwise: the
// configuration directory of the operating system, under a directory of ours.
func ConnectionsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding the configuration directory of this system: %w", err)
	}

	return filepath.Join(dir, "hermes", "connections.toml"), nil
}

// Load reads the saved connections.
//
// A file that is not there is an empty list and not a fault: on a first run
// nobody has saved anything yet, and reporting that as an error would greet
// someone with a failure for having done nothing wrong.
func (c *Connections) Load() ([]conn.Config, error) {
	file, err := os.Open(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", c.path, err)
	}
	defer func() { _ = file.Close() }()

	configs, err := profile.ReadConnections(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.path, err)
	}

	return configs, nil
}

// Save writes the whole list, replacing whatever was there.
//
// The bytes go to a temporary file beside the real one, are flushed to the
// disk, and are then renamed over it. Both halves are needed and they answer
// different questions. The rename is what makes the replacement atomic for
// anything else reading the file: nobody ever sees it half written. The flush
// is what makes it survive the machine losing power, because a rename orders
// the directory entry and says nothing about whether the bytes it now points at
// ever left the page cache — without it the file that replaces a good list can
// be a file of zeros.
func (c *Connections) Save(connections []conn.Config) error {
	directory := filepath.Dir(c.path)
	if err := os.MkdirAll(directory, dirMode); err != nil {
		return fmt.Errorf("creating %s: %w", directory, err)
	}

	temporary, err := os.CreateTemp(directory, ".connections-*.toml")
	if err != nil {
		return fmt.Errorf("creating a temporary file beside %s: %w", c.path, err)
	}
	// Harmless once the rename has happened, and the only thing that clears the
	// file up when anything below fails.
	defer func() { _ = os.Remove(temporary.Name()) }()

	if err := write(temporary, connections); err != nil {
		return err
	}

	if err := os.Rename(temporary.Name(), c.path); err != nil {
		return fmt.Errorf("replacing %s: %w", c.path, err)
	}

	return nil
}

func write(file *os.File, connections []conn.Config) error {
	defer func() { _ = file.Close() }()

	// os.CreateTemp already creates the file private to this user on Unix; this
	// is what says so out loud and what sets it where the default is wider.
	if err := file.Chmod(fileMode); err != nil {
		return fmt.Errorf("setting the permissions of %s: %w", file.Name(), err)
	}

	if err := profile.WriteConnections(file, connections); err != nil {
		return err
	}

	// Before the close and therefore before the rename: this is the line that
	// turns "the previous list survives a power cut" from a hope into a fact.
	if err := file.Sync(); err != nil {
		return fmt.Errorf("flushing %s to the disk: %w", file.Name(), err)
	}

	// Closed here rather than only by the defer, because a write that fails on
	// close has still failed and a rename would publish the damage.
	if err := file.Close(); err != nil {
		return fmt.Errorf("finishing %s: %w", file.Name(), err)
	}

	return nil
}
