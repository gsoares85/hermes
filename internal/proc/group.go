// Package proc owns the life of a subprocess: it starts one in a group of its
// own, waits for it, and kills the group.
//
// It is infrastructure and lives outside the core, for the reason ADR-0016
// gives: creating a process in a group means SysProcAttr on one system and a
// Job Object on another, and a domain should not need an operating system to
// be testable. The dependency gate forbids both internal/core and internal/ui
// from importing it; the binaries wire it in.
//
// Starting in a group is not a convenience. pg_restore -j starts workers, and
// killing only the parent leaves them orphaned and still writing to the
// database after the person cancelled — the failure nobody sees until a
// restore somebody stopped has half finished.
//
// It is deliberately not the package that hands a credential over. That is
// internal/credential, which decorates a command before it starts and cleans
// up after it ends. Each treats one aspect of the same exec.Cmd, neither knows
// the other, and the binary composes them.
package proc

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
)

// ErrNotStarted is a command asked to do something only a running process can.
// Killing or waiting for one that was never started is a mistake in the
// caller, and a silent success would hide it.
var ErrNotStarted = errors.New("the process was never started")

// Command is a subprocess that lives in a process group of its own.
//
// The zero value is not usable; New makes one.
type Command struct {
	cmd *exec.Cmd

	mu      sync.Mutex
	started bool
	// group is what the operating system uses to name the group, on the one
	// that needs a handle to name it. Unix leaves it zero: there the group is
	// named by the identifier of the process that leads it.
	group uintptr
}

// New prepares a command. Nothing is started.
func New(name string, args ...string) *Command {
	return &Command{cmd: exec.Command(name, args...)} //nolint:gosec // the caller chose the binary
}

// Cmd is the command as os/exec sees it, for whoever has something to attach
// before it starts.
//
// Exposed rather than wrapped because two things legitimately need it and
// neither belongs here: internal/credential puts a password in the
// environment, and the caller plugs the job's log into the output. Copying
// the surface of exec.Cmd to hide it would be copying it badly, and this
// package is the outside of the operating system already.
//
// Changing it after Start does nothing, which is exec's rule and not one this
// adds.
func (c *Command) Cmd() *exec.Cmd {
	return c.cmd
}

// Start begins the process, in a group of its own.
func (c *Command) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.prepare()

	if err := c.cmd.Start(); err != nil {
		// The name is in the message because "fork/exec: no such file" says
		// nothing about which binary Hermes went looking for, and that is the
		// part the person has to act on — the client tools are not packaged,
		// so a missing one is an ordinary outcome here.
		return fmt.Errorf("starting %s: %w", c.cmd.Path, err)
	}

	c.started = true

	return c.adopt()
}

// Wait waits for the process to end.
//
// The error is what os/exec reports for a process that ended badly, killed
// included: the three systems do not agree on the words, and translating them
// here would be inventing a vocabulary the caller would then have to learn.
func (c *Command) Wait() error {
	if !c.running() {
		return ErrNotStarted
	}

	if err := c.cmd.Wait(); err != nil {
		return fmt.Errorf("waiting for %s: %w", c.cmd.Path, err)
	}

	return nil
}

// Kill ends the process and every process it started.
//
// Killing one that has already ended is not an error. Cancelling twice is one
// cancellation, and the second attempt lands on a process that is already
// gone.
func (c *Command) Kill() error {
	if !c.running() {
		return ErrNotStarted
	}

	return c.killGroup()
}

func (c *Command) running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.started
}
