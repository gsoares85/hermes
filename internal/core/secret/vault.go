// Package secret defines the vault contract and the reference that addresses a
// secret in it.
//
// It belongs to the core layer: it must never import UI or framework packages,
// and — the reason it exists at all — it must never import a keychain. The
// contract is defined here, where it is consumed; the implementations live in
// internal/vault, which is the only place in the repository allowed to talk to
// the keychain of an operating system. See ADR-0010.
package secret

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Service is the name every secret of this application is filed under.
//
// A keychain is shared by everything the person runs, so the service is what
// separates our items from everyone else's. It is a constant rather than a
// parameter because two spellings of it would be two vaults, and the second one
// would look empty.
const Service = "Hermes"

// Errors of the vault. They are sentinels because the layer above reacts to
// each one differently: a secret that is not there means asking for the
// password, a vault that is not there means telling the person why.
var (
	// ErrNotFound is answered by a reference that holds no secret.
	ErrNotFound = errors.New("no secret is stored under this reference")

	// ErrUnavailable is answered when the vault itself cannot be reached — no
	// Secret Service on the session bus, a keychain that will not unlock. It
	// is distinct from ErrNotFound on purpose: one is a missing password, the
	// other is a missing place to keep passwords, and only the second is worth
	// telling the person to go and install something.
	ErrUnavailable = errors.New("the vault is unavailable")

	// ErrInvalidRef is a reference that cannot address anything.
	ErrInvalidRef = errors.New("invalid secret reference")

	// ErrEmptySecret refuses to store nothing under a name. A connection
	// without a password stores no secret at all; storing an empty one would
	// report success and read back a value that fails to authenticate for a
	// reason nobody can see.
	ErrEmptySecret = errors.New("refusing to store an empty secret")
)

// Ref addresses one secret. It is not itself a secret: it may be logged,
// rendered into an error and written to the connections file.
type Ref struct {
	// Service groups every secret of one application.
	Service string
	// Account identifies the secret within the service. For a connection it
	// is the identifier of the connection — never its name, which the person
	// can change and can repeat.
	Account string
}

// ConnectionRef addresses the password of a saved connection.
//
// The convention lives in one function because getting it wrong later is not a
// bug that shows up as a failure: it is a password that is still in the keychain
// under a name nothing looks for any more.
func ConnectionRef(id string) Ref {
	return Ref{Service: Service, Account: id}
}

// Validate reports whether the reference can address a secret.
//
// Beyond being present, both fields have to survive the trip through a C API:
// the keychain of macOS and the credential store of Windows both take
// NUL-terminated strings, so an account carrying a NUL would be truncated, and
// two different connections would quietly address one secret — the second
// overwriting the first. Control characters are refused for the same family of
// reason, one layer up: they make an item unreadable in the keychain viewer the
// person is told to open when something goes wrong.
func (r Ref) Validate() error {
	if err := validField("service", r.Service); err != nil {
		return err
	}

	return validField("account", r.Account)
}

func validField(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: no %s", ErrInvalidRef, name)
	}

	if index := strings.IndexFunc(value, isControl); index >= 0 {
		return fmt.Errorf("%w: the %s contains the control character %q", ErrInvalidRef, name, value[index])
	}

	return nil
}

func isControl(char rune) bool {
	return char < 0x20 || char == 0x7f
}

// String renders the reference. It carries no secret, by construction: neither
// field is one.
func (r Ref) String() string {
	return r.Service + "/" + r.Account
}

// Vault stores secrets where the operating system keeps them.
//
// Every method takes a context because reading a keychain is not a map lookup:
// on macOS and on Linux it can open a dialog and wait for a person, and a call
// that cannot be given up on is a window that hangs. Implementations must honour
// cancellation even when their own store could answer instantly, or the core
// would be proven against a double that is easier to satisfy than the real one.
//
// A secret is a string rather than a byte slice, matching what the connection
// model and the driver seam already carry. Bytes would allow wiping the value
// after use, which a Go string cannot offer; that is worth revisiting the day
// the whole path from form to driver can hold bytes, and is theatre before then.
type Vault interface {
	// Get returns the secret stored under the reference, or ErrNotFound.
	Get(ctx context.Context, ref Ref) (string, error)

	// Set stores the secret, replacing whatever the reference held. It refuses
	// an empty value with ErrEmptySecret.
	Set(ctx context.Context, ref Ref, value string) error

	// Delete removes the secret, answering ErrNotFound when there was none.
	// Removing a connection whose secret has already gone is a normal outcome
	// and the caller ignores that error; a reference computed wrongly is a
	// bug, and a silent success would hide it.
	Delete(ctx context.Context, ref Ref) error
}
