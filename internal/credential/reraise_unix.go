//go:build !windows

package credential

import (
	"os"
	"syscall"
	"time"
)

// How long to stay alive after handing the signal back, waiting to be killed by
// it.
//
// kill(2) makes the signal pending and returns; the kernel delivers it to
// whichever thread of this process has it unblocked, which in a Go program is
// rarely the one that sent it. Returning as soon as the send succeeds is
// therefore racing the death — a race that is lost often enough on a busy
// machine to matter, in a process whose cleanup has already run and whose
// handler is already unregistered.
//
// Generous because it costs nothing when it is not needed: the death arrives
// microseconds into this wait, and the wait ends with it.
const whileTheSignalArrives = 5 * time.Second

// reraise hands the signal back to this process, now that the default
// disposition has been restored. It does not return.
//
// Sending it again rather than calling os.Exit is what makes the process die
// the way it would have died with no handler installed: killed by that signal,
// so a shell reports 128+n and a supervisor sees a termination rather than a
// program that chose to stop.
func reraise(interrupted os.Signal) {
	number, ok := interrupted.(syscall.Signal)
	if !ok {
		os.Exit(1)
	}

	if err := syscall.Kill(os.Getpid(), number); err == nil {
		time.Sleep(whileTheSignalArrives)
	}

	// The signal could not be sent, or was sent and ended nothing. Staying
	// alive is the one outcome this function exists to prevent, so the process
	// leaves with the status its death would have been reported as.
	os.Exit(statusAfter(number))
}

// The status a shell reports for a program killed by a signal.
func statusAfter(number syscall.Signal) int {
	return 128 + int(number)
}
