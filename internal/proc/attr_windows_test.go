//go:build windows

package proc

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// Something a caller might legitimately have set, that this package does not
// write: a creation flag of its own, beside the one added here.
func attrWithSomethingElse() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}

func somethingElseSurvived(attr *syscall.SysProcAttr) bool {
	return attr != nil &&
		attr.CreationFlags&windows.CREATE_NO_WINDOW != 0 &&
		attr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP != 0
}
