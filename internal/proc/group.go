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

	mu     sync.Mutex
	waited sync.Once
	// waitErr is what the one wait answered, for every caller that arrives
	// after it.
	waitErr error
	started bool
	// finished says the child has been waited for and collected. Its
	// identifier means nothing from that moment: the kernel is free to give it
	// to somebody else, and signalling it would reach whoever got it.
	finished bool
	// group is how the operating system names the group, written once when the
	// process starts and never read from the process afterwards. On Unix it is
	// the identifier of the process that leads the group, which is the child
	// itself; on Windows it is the handle of the job object holding it.
	//
	// Captured rather than asked for at the moment of killing, because by then
	// the child may have been collected and its identifier reused.
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

	return c.start(c.adopt)
}

// start is Start with the adoption to perform handed in, so that a test can
// see what happens when it fails — which is a thing only Windows does, and
// only on a machine whose policy refuses to open a process.
func (c *Command) start(adopt func() error) error {
	c.prepare()

	if err := c.cmd.Start(); err != nil {
		// The name is in the message because "fork/exec: no such file" says
		// nothing about which binary Hermes went looking for, and that is the
		// part the person has to act on — the client tools are not packaged,
		// so a missing one is an ordinary outcome here.
		return fmt.Errorf("starting %s: %w", c.cmd.Path, err)
	}

	if err := adopt(); err != nil {
		// The process is running and nothing holds its children. Reporting a
		// failure and leaving it started would be the worst of both: the
		// caller believes nothing began, so it never waits for it, and a Kill
		// it does send lands on a group that was never formed and reports
		// success having killed nothing.
		//
		// So the start is undone. The process this function began is the one
		// it takes back, and nothing it could not hold is left running.
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()

		return err
	}

	c.started = true

	return nil
}

// Wait waits for the process to end.
//
// The error is what os/exec reports for a process that ended badly, killed
// included: the three systems do not agree on the words, and translating them
// here would be inventing a vocabulary the caller would then have to learn.
//
// Waiting twice answers what the first wait answered rather than the complaint
// os/exec makes about it. A job that cancels while its own goroutine is
// already waiting is two callers arriving at the same process, and neither of
// them is a mistake.
func (c *Command) Wait() error {
	if !c.running() {
		return ErrNotStarted
	}

	c.waited.Do(func() { c.waitErr = c.cmd.Wait(); c.release() })

	err := c.waitErr

	if err != nil {
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
	// Held across the signal rather than only to read the flags: a process
	// that has been collected has an identifier the kernel may have given to
	// somebody else, and the lock is what stops Wait from marking it collected
	// while this is signalling.
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started {
		return ErrNotStarted
	}

	if c.finished {
		// Waited for and gone. There is nothing to kill, and the identifier it
		// used to have is not ours to signal any more.
		return nil
	}

	return c.killGroup()
}

// release marks the process collected and lets go of whatever the operating
// system needed held while it was alive.
//
// Marked under the lock Kill holds while it signals, so that the two cannot
// overlap: from here the identifier belongs to the kernel again, and a signal
// to it would reach whoever the kernel gave it to next.
func (c *Command) release() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.finished = true
	c.letGo()
}

func (c *Command) running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.started
}
