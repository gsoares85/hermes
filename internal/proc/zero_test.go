package proc

import (
	"os"
	"syscall"
)

// The signal that asks whether a process is there without touching it. On
// Windows it is not a signal at all, and Signal answers an error for a process
// that has ended — which is the same question, answered.
var zeroSignal os.Signal = syscall.Signal(0)
