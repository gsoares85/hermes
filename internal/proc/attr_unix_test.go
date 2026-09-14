//go:build !windows

package proc

import "syscall"

// Something a caller might legitimately have set, that this package does not
// write: a session of the process's own.
func attrWithSomethingElse() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: false, Foreground: false, Pgid: 0, Noctty: true}
}

func somethingElseSurvived(attr *syscall.SysProcAttr) bool {
	return attr != nil && attr.Noctty && attr.Setpgid
}
