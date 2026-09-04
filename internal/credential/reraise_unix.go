//go:build !windows

package credential

import (
	"os"
	"syscall"
)

// reraise hands the signal back to this process, now that the default
// disposition has been restored.
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

	if err := syscall.Kill(os.Getpid(), number); err != nil {
		// Nothing left to try, and staying alive is the one outcome this
		// function exists to prevent.
		os.Exit(1)
	}
}
