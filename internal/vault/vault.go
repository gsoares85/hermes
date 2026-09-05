// Package vault implements the secret vault of internal/core/secret against the
// store each operating system keeps passwords in, and chooses between them.
//
// It is the only place in the repository allowed to import a keychain library.
// The contract lives in internal/core/secret because that is where it is
// consumed; the cgo, the D-Bus and the Win32 calls live here because a domain
// that needs an unlocked keyring to be testable is not a domain. The dependency
// gate forbids both the core and the UI from reaching in here, and the binaries
// hand the vault down exactly as they hand down the driver. See ADR-0010.
package vault

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/gsoares85/hermes/internal/core/secret"
	"github.com/gsoares85/hermes/internal/vault/system"
)

// BackendMemory names the vault that lives in the process and dies with it. It
// is what Open falls back to, and the only backend that keeps nothing.
const BackendMemory = "in-memory"

// detectionTimeout bounds the one call that asks the operating system whether
// it has a store for us.
//
// It is a ceiling on a hang, not the expected cost: a keyring that is running
// answers in milliseconds. The number matters because the two platforms that
// can be slow are the two that can be interactive — a Secret Service that has
// to be activated on the session bus, a keychain that opens a dialog — and a
// window that waits for either is a window that looks broken.
const detectionTimeout = 2 * time.Second

// Vault is the contract plus the release of whatever the platform holds open.
//
// Close is here and not on secret.Vault because it is a fact about an
// implementation, not about the contract: the core asks for a secret and has no
// business knowing that answering it costs a connection to a session bus. The
// binary that opened the vault is the one that closes it.
type Vault interface {
	secret.Vault
	io.Closer
}

// Status describes which store ended up behind the vault.
//
// Warning is empty exactly when the store of the operating system answered. It
// is prose rather than a code because it is shown to a person: the whole point
// of this type is that a machine without a keyring is told so, in words, rather
// than quietly forgetting every password it is given.
type Status struct {
	Backend string
	Warning string
}

// Open returns the vault this machine can offer, and never fails: a system with
// no usable store gets the in-memory vault and a warning that says so.
//
// There is no error to return because there is no decision left for the caller
// to make. Refusing to run without a keyring would make Hermes unusable in a
// container and on a lean Linux, over a convenience; the honest outcome is a
// product that still connects and a person who is told their passwords last
// until they quit.
func Open(ctx context.Context) (Vault, Status) {
	return open(ctx, openSystem, detectionTimeout)
}

// openSystem is the one line that turns the store of the platform into the
// vault this package hands out. The two interfaces have the same method set, so
// this is an assignment and not a conversion; it exists because Go will not let
// a function returning one be used where a function returning the other is
// expected.
func openSystem(ctx context.Context) (Vault, error) {
	opened, err := system.Open(ctx)
	if err != nil {
		return nil, err
	}

	return opened, nil
}

// opener is the seam the platform files fill in, and the one the tests replace.
type opener func(context.Context) (Vault, error)

func open(ctx context.Context, store opener, timeout time.Duration) (Vault, Status) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	answers := make(chan answer, 1)
	go func() {
		vault, err := store(ctx)
		answers <- answer{vault: vault, err: err}
	}()

	select {
	case got := <-answers:
		if got.err != nil {
			return fallback(got.err)
		}

		return got.vault, Status{Backend: system.Backend}
	case <-ctx.Done():
		// The deadline releases the caller, not the call: the probe is still
		// running, and whatever it opens is a connection nothing else will ever
		// have a reference to.
		go discard(answers)

		return fallback(ctx.Err())
	}
}

type answer struct {
	vault Vault
	err   error
}

func discard(answers <-chan answer) {
	if got := <-answers; got.err == nil {
		_ = got.vault.Close()
	}
}

func fallback(reason error) (Vault, Status) {
	return session{secret.NewMemory()}, Status{
		Backend: BackendMemory,
		Warning: fmt.Sprintf(
			"Hermes could not reach %s: %v. Passwords are kept in memory for this session only, "+
				"and will be asked for again the next time Hermes starts. %s",
			system.Backend, reason, system.Advice),
	}
}

// session is the in-memory vault with the Close the platform stores need. There
// is nothing to release, and saying so once here is what lets every caller
// close a vault without asking which one it got.
type session struct {
	*secret.Memory
}

func (session) Close() error { return nil }
