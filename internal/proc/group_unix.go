//go:build !windows

package proc

import (
	"errors"
	"fmt"
	"syscall"
)

// prepare asks the kernel to put the child in a group of its own.
//
// Setpgid makes the child the leader of a new group, so its identifier is the
// group's identifier too. Everything it starts inherits the group, which is
// what makes one signal reach the workers of a pg_restore -j as well as the
// process Hermes started.
func (c *Command) prepare() {
	c.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// adopt writes down which group the kernel made, which is no work at all here:
// Setpgid makes the child the leader of its own group, so the group's
// identifier is the child's. It is the Windows half of this pair that has real
// work to do after the fact.
//
// Written down rather than asked for later, because "later" is the moment the
// process may already have been collected — and then the identifier belongs to
// whoever the kernel gave it to next.
func (c *Command) adopt() error {
	c.group = uintptr(c.cmd.Process.Pid) //nolint:gosec // a pid is not a signed quantity

	return nil
}

// letGo has nothing to release here: a process group is not a thing the kernel
// hands out a handle to, and it stops existing when the processes in it do.
//
// The caller holds the lock.
func (c *Command) letGo() {}

// killGroup signals the whole group.
//
// The negative identifier is what makes it the group rather than the leader
// alone — the difference between a cancelled restore and a cancelled restore
// whose workers carry on writing.
//
// SIGKILL rather than SIGTERM: the caller is cancelling, and what is left
// behind is removed by the cleanup the job runs afterwards. A signal the child
// may choose to ignore would make the deadline on that cleanup the only thing
// standing between a person and a button that did nothing.
func (c *Command) killGroup() error {
	group := int(c.group) //nolint:gosec // it is the pid this package wrote down at Start

	if err := syscall.Kill(-group, syscall.SIGKILL); err != nil {
		// The same race, one step later.
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}

		return fmt.Errorf("killing the process group of %s: %w", c.cmd.Path, err)
	}

	return nil
}
