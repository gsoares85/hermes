//go:build windows

package privatedir

import "io/fs"

// narrow has nothing to narrow on Windows, and saying so out loud is the point
// of the file.
//
// Who may read a directory there is decided by its access control list, which
// a Hermes directory inherits from the profile of the user it sits under, and
// os.Chmod reaches none of it: on Windows it moves the read-only attribute and
// nothing else. Calling it here would report success for a change that never
// happened, which is worse than the no-op it would be replacing.
//
// The inherited list is what the Windows tests assert, by SID rather than by a
// name the system translates.
func narrow(string, fs.FileMode) error { return nil }
