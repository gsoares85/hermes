//go:build windows

package credential

import (
	"math"
	"syscall"
)

// The exit code Windows reports for a process that has not exited. It is the
// reason opening a handle is not enough on its own: a handle can still be
// opened for a process that has already stopped.
const stillActive = 259

// alive reports whether a process with this identifier is still running.
//
// A handle that cannot be opened is a process that is gone. One that can be
// opened is asked for its exit code, because Windows keeps a stopped process
// addressable for as long as anything holds a handle to it, and a sweep that
// took that for "still running" would never remove anything.
func alive(pid int) bool {
	// A Windows process identifier is a DWORD, so anything outside that range
	// names no process — and the conversion below would silently wrap it into
	// one that does.
	if pid <= 0 || uint64(pid) > math.MaxUint32 {
		return false
	}

	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = syscall.CloseHandle(handle) }()

	var code uint32
	if err := syscall.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}

	return code == stillActive
}
