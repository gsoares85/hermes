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
// The handle is asked for its exit code instead. A process that has not exited
// reports STILL_ACTIVE, which Windows defines as 259 and which x/sys does not
// name — so it is named here, where the one reader of it is.
const stillActive = 259

func running(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}

	return code == stillActive
}
