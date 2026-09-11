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

// adopt has nothing to do here: the group exists from the moment the process
// does, because the kernel made it as part of starting it. It is the Windows
// half of this pair that has work to do after the fact.
func (c *Command) adopt() error {
	return nil
}

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
	group, err := syscall.Getpgid(c.cmd.Process.Pid)
	if err != nil {
		// There is no group because there is no process: it ended between the
		// decision to kill it and the attempt. Nothing left to do.
		return nil
	}

	if err := syscall.Kill(-group, syscall.SIGKILL); err != nil {
		// The same race, one step later.
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}

		return fmt.Errorf("killing the process group of %s: %w", c.cmd.Path, err)
	}

	return nil
}
