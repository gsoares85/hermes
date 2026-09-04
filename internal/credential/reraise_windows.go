//go:build windows

package credential

import "os"

// The status a shell reports for a program ended by Ctrl-C. Windows offers no
// way to send a signal to the current process, so the exit is direct and the
// number is chosen to say the same thing the Unix path says by dying.
const interruptedStatus = 130

// reraise ends this process now that the cleanup has run.
func reraise(os.Signal) {
	os.Exit(interruptedStatus)
}
