//go:build !windows

package postgres

import (
	"errors"
	"syscall"
)

// isRefused reports a closed port. Everywhere but Windows the portable error
// number is what the kernel returns.
func isRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}

// isDropped reports a connection that was established and then lost.
func isDropped(err error) bool {
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED)
}
