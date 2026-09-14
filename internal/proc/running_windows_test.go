//go:build windows

package proc

import "golang.org/x/sys/windows"

// running asks whether a process is there, without touching it.
//
// Not through os.Process.Signal, which is the obvious way and the wrong one:
// on Windows it answers an error for every signal but Kill, whatever the
// process is doing. A check written on it reports that everything is gone,
// always — so a case waiting for a process to end would pass without ever
// looking at one.
//
// Not through the exit code either, which is the next thing to reach for and
// is ambiguous: a process that has not ended reports 259, and one that ended
// by returning 259 reports the same thing. Windows says so itself, in the
// words "an application should not use STILL_ACTIVE as an error code" — which
// is advice to the program being watched, and not something a watcher can
// hold it to.
//
// The handle is waited on instead, for no time at all. A process that is still
// running is not signalled and the wait times out; one that has ended is
// signalled at once. That is an answer about the process rather than about a
// number it might one day return.
func running(pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	state, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return false
	}

	return state == uint32(windows.WAIT_TIMEOUT)
}
