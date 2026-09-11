//go:build windows

package proc

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// prepare puts the child at the head of its own console process group.
//
// It is half the story on Windows and the weaker half: a console group governs
// which processes a Ctrl+Break reaches, not which ones die together. What
// actually holds the tree is the Job Object below. The flag is set anyway so
// that a Ctrl+C in whatever console Hermes was started from does not reach a
// backup running behind the window.
func (c *Command) prepare() {
	c.cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

// adopt puts the process into a Job Object, which is what makes its children
// die with it.
//
// Windows has no process group that a signal reaches, and killing a parent
// leaves its children running with no relationship the system will act on. A
// Job Object is the relationship: everything the process starts joins the job,
// and terminating the job terminates all of it.
//
// KILL_ON_JOB_CLOSE is what covers the case nothing else does — Hermes itself
// being killed. The job's last handle closes when this process dies, and the
// tree goes with it rather than outliving the application that started it.
//
// The process is assigned after it has started rather than before, because Go
// hands back no thread handle to resume a suspended one with. The gap is the
// microseconds between CreateProcess returning and the assignment, and a child
// started inside it escapes the job. pg_dump and pg_restore do not fork that
// fast; if one ever did, the fix is a suspended start, which needs more of the
// creation than os/exec exposes.
func (c *Command) adopt() error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("creating the job object for %s: %w", c.cmd.Path, err)
	}

	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}

	// The Win32 call takes the struct by address and its size, which is what
	// unsafe is for here: there is no shape of this API that does not, and the
	// struct is one this function owns and keeps alive across the call.
	if _, set := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), //nolint:gosec // the only shape this Win32 call has
		uint32(unsafe.Sizeof(limits)),
	); set != nil {
		_ = windows.CloseHandle(job)

		return fmt.Errorf("configuring the job object for %s: %w", c.cmd.Path, set)
	}

	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(c.cmd.Process.Pid), //nolint:gosec // a pid is not a signed quantity
	)
	if err != nil {
		_ = windows.CloseHandle(job)

		return fmt.Errorf("opening %s to put it in a job object: %w", c.cmd.Path, err)
	}

	defer func() { _ = windows.CloseHandle(process) }()

	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)

		return fmt.Errorf("putting %s in a job object: %w", c.cmd.Path, err)
	}

	c.group = uintptr(job)

	return nil
}

// killGroup terminates the job, and with it everything the process started.
func (c *Command) killGroup() error {
	if c.group == 0 {
		return nil
	}

	// A job whose processes have all ended is terminated again without
	// complaint, which is what makes cancelling twice one cancellation.
	if err := windows.TerminateJobObject(windows.Handle(c.group), 1); err != nil {
		return fmt.Errorf("terminating the job object of %s: %w", c.cmd.Path, err)
	}

	return nil
}
