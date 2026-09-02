//go:build windows

package postgres

import (
	"errors"
	"syscall"
)

// wsaeconnrefused is the Windows sockets number for a refused connection.
//
// It is written out because the standard library does not export the WSA error
// numbers on Windows, and pulling in golang.org/x/sys for a single constant is
// a dependency this package does not otherwise need. The value is fixed by the
// platform and verified by the test that dials a closed port.
const (
	wsaeconnrefused = syscall.Errno(10061)
	wsaeconnreset   = syscall.Errno(10054)
	wsaeconnaborted = syscall.Errno(10053)
)

// isRefused reports a closed port.
//
// Windows answers with its own sockets error number, and Go does not map
// WSAECONNREFUSED onto the portable ECONNREFUSED, so errors.Is against the
// portable one is false on this platform. Matching the message instead is not
// an option: Windows localises it, and on a Spanish machine it reads "el equipo
// de destino denegó expresamente dicha conexión".
func isRefused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}

	var errno syscall.Errno

	return errors.As(err, &errno) && errno == wsaeconnrefused
}

// isDropped reports a connection that was established and then lost. Windows
// splits this into a reset by the peer and an abort by the local stack, and a
// stopped container produces the second — "se ha anulado una conexión
// establecida por el software en su equipo host" on a Spanish machine, which is
// why the number is read rather than the text.
func isDropped(err error) bool {
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) {
		return true
	}

	var errno syscall.Errno

	return errors.As(err, &errno) && (errno == wsaeconnreset || errno == wsaeconnaborted)
}
