//go:build windows

package credential

import (
	"math"
	"syscall"
)

// The exit code Windows reports for a process that has not exited. It is the
// reason opening a handle is not enough on its own: a handle can still be
// opened for a process that has already stopped.
const (
	stillActive = 259

	// PROCESS_QUERY_LIMITED_INFORMATION. Not in syscall, which only carries
	// the wider right.
	processQueryLimitedInformation = 0x1000
)

// alive reports whether a process with this identifier is still running.
//
// A handle that cannot be opened is a process that is gone. One that can be
// opened is asked for its exit code, because Windows keeps a stopped process
// addressable for as long as anything holds a handle to it, and a sweep that
// took that for "still running" would never remove anything.
//
// The limited right rather than the full one, because they fail differently and
// only one of the two failures is safe here. PROCESS_QUERY_INFORMATION is
// refused for a process this user may not inspect, and this function reports
// that as "gone" — which would have the sweep delete a password file still in
// use. The limited right is granted across those boundaries, so "cannot open"
// means the process really has ended. It is the same choice the Unix side makes
// by treating EPERM as alive.
func alive(pid int) bool {
	// A Windows process identifier is a DWORD, so anything outside that range
	// names no process — and the conversion below would silently wrap it into
	// one that does.
	if pid <= 0 || uint64(pid) > math.MaxUint32 {
		return false
	}

	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
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
