// Package credential hands a database password to a child process without ever
// putting it on a command line.
//
// Everything Hermes shells out to — pg_dump, pg_restore, psql — accepts a
// password from the environment or from a password file, and none of them
// accepts one as an argument, for the reason this package exists: on all three
// target systems the command line of a process is readable by every other
// process of the same user, and on Linux by anything that can open /proc. A
// password on a command line is a password published to the machine.
//
// It is infrastructure and lives outside the core: handing a credential over
// means environments, file modes and signals, none of which a domain should
// have to be running to be testable. The dependency gate forbids both
// internal/core and internal/ui from importing it; the binaries wire it in.
//
// Two routes are offered, and the environment is the default one because it
// touches no disk: there is no file to remove, and therefore no file to leak
// when the process dies in a way no code of ours can handle. The password file
// is for the case the environment cannot serve — more than one server in a
// single operation — and is guarded by the three layers described in sweep.go.
package credential

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// The variables libpq reads a credential from. Both are named here because both
// have to be dealt with on every handoff: setting one while leaving the other
// inherited from the session is how a child authenticates with a password
// nobody in this process chose.
const (
	passwordVariable = "PGPASSWORD"
	passfileVariable = "PGPASSFILE"
)

// Errors of this package. They are sentinels because the difference matters to
// the caller: one is a password it should ask for again, the other is a target
// it assembled wrongly.
var (
	// ErrUnrepresentable is a value that cannot survive the trip to a child
	// process intact.
	ErrUnrepresentable = errors.New("this value cannot be handed to a child process")

	// ErrIncompleteTarget is a target libpq could never match a record to.
	ErrIncompleteTarget = errors.New("incomplete target")
)

// Handoff is a credential prepared for a child process: what to add to its
// environment, and what to remove afterwards.
//
// The zero value is the handoff that hands nothing over, and it is usable:
// Apply leaves the environment alone and Release succeeds. That is what lets a
// caller defer Release without first asking which kind of handoff it got.
type Handoff struct {
	env  []string
	path string
}

// InEnvironment prepares the password as an environment variable of the child.
//
// This is the default route. Nothing is written, so nothing can be left behind:
// the environment of a process is readable by its own user and by root, which
// is the same audience that can already read the password file of that user.
func InEnvironment(password string) (Handoff, error) {
	if password == "" {
		return Handoff{}, nil
	}

	if err := representable("password", password); err != nil {
		return Handoff{}, err
	}

	return Handoff{env: []string{passwordVariable + "=" + password}}, nil
}

// Apply gives the command the credential, replacing any that the session
// already had.
//
// The replacement is the point. libpq reads PGPASSWORD in preference to
// PGPASSFILE, so a variable inherited from the terminal Hermes was started
// from would beat the file this package just wrote, and the child would
// authenticate as somebody's leftover shell export.
func (h Handoff) Apply(command *exec.Cmd) {
	if len(h.env) == 0 {
		return
	}

	command.Env = append(withoutCredentials(command.Environ()), h.env...)
}

// Release removes whatever the handoff wrote. A file that has already gone —
// released twice, or swept by another process — is the outcome asked for.
func (h Handoff) Release() error {
	if h.path == "" {
		return nil
	}

	if err := os.Remove(h.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", h.path, err)
	}

	return nil
}

// String describes the handoff without describing the credential.
//
// It exists so that %v cannot print one. The path is safe to name: it is a
// random file name in a directory of ours, and saying which route was taken is
// exactly what someone reading a log about a failed dump needs to know.
func (h Handoff) String() string {
	switch {
	case h.path != "":
		return "credential handed over in " + h.path
	case len(h.env) > 0:
		return "credential handed over in the environment"
	default:
		return "no credential handed over"
	}
}

// withoutCredentials drops every entry libpq would read a credential from.
//
// The comparison ignores case because the environment of Windows does, so
// "PgPassword" there is the same variable and would survive an exact match. On
// Unix it is a different variable, one that libpq does not read and nobody
// sets; dropping it too costs nothing and keeps one rule instead of two.
func withoutCredentials(environment []string) []string {
	kept := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, passwordVariable) || strings.EqualFold(name, passfileVariable) {
			continue
		}
		kept = append(kept, entry)
	}

	return kept
}

// representable refuses what cannot be written to a password file.
//
// A line break ends a record there, and a NUL ends a string in every C API
// between here and the server. The environment could carry a line break, but
// refusing it on both routes is what keeps them interchangeable: a caller that
// moves from one to the other must not discover then that the password it has
// been using all along cannot be represented.
func representable(field, value string) error {
	if strings.ContainsAny(value, "\n\r\x00") {
		return fmt.Errorf("%w: the %s contains a line break or a NUL", ErrUnrepresentable, field)
	}

	return nil
}
