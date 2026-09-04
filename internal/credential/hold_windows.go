//go:build windows

package credential

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

// Not in the syscall package, which carries the other two sharing flags but not
// this one.
const fileShareDelete = 0x00000004

// hold takes the claim that says this file is in use, and answers what keeps
// it. Closing that gives the claim up; the file it was given is the caller's to
// close as usual.
//
// Windows needs no lock: an open handle already refuses a second opener that
// asks for exclusive access, and the operating system closes every handle when
// a process ends — on a crash and on a kill as much as on a normal exit.
//
// The sharing flags are the whole design, and each one is there for something
// that has to keep working. Reading, because pg_dump has to read the file.
// Writing, because the handle the content is written through is still open when
// this is taken. Deleting, because this process has to be able to remove the
// file while still holding it — Go's own file handles do not share deletion,
// which is why this is a handle of its own rather than the one above.
func hold(file *os.File) (io.Closer, error) {
	name, err := syscall.UTF16PtrFromString(file.Name())
	if err != nil {
		return nil, fmt.Errorf("naming %s: %w", file.Name(), err)
	}

	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|fileShareDelete, nil,
		syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("claiming %s: %w", file.Name(), err)
	}

	return held(handle), nil
}

// held is the open handle, and closing it is what gives the claim up.
type held syscall.Handle

func (h held) Close() error {
	if err := syscall.CloseHandle(syscall.Handle(h)); err != nil {
		return fmt.Errorf("giving up a password file: %w", err)
	}

	return nil
}

// orphaned reports whether nothing holds the file any more.
//
// It asks for the file with no sharing at all, which fails while any other
// handle is open — including one this very process holds, which is what stops
// a second Hermes, and this one, sweeping a file an operation is still using.
// The file stays readable to a child all the while: the probe is refused
// because of what it asks for, not because of how the owner opened it.
func orphaned(path string) bool {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false
	}

	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil,
		syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return false
	}
	_ = syscall.CloseHandle(handle)

	return true
}
