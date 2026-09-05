// Package system is every implementation of the vault that talks to the store
// an operating system already keeps passwords in.
//
// It is a package of its own so that it can be excluded from the coverage count
// honestly. Only one of these files compiles on any given machine, and all of
// them are exercised by the keychain job, which runs on three runners and feeds
// no profile — so counting their statements while ignoring their coverage would
// drag the number down for code that is in fact tested. Excluding the vault
// whole used to take the platform-independent half with it: the choosing, the
// fallback and the deferred opening, which have a suite of their own and hold
// the concurrency worth counting.
//
// It is also the only place in the repository allowed to import a keychain
// library. See ADR-0010, and the dependency gate, which forbids the core and
// the UI from reaching anywhere under internal/vault.
package system

import (
	"io"

	"github.com/gsoares85/hermes/internal/core/secret"
)

// Vault is what a store of the operating system answers: the contract, plus the
// release of whatever the platform holds open.
//
// It is declared here as well as in the package above rather than imported from
// it, because importing it would be a cycle: internal/vault is what chooses
// between this and the in-memory fallback. Two identical method sets satisfy
// each other, so the value crosses without a conversion.
type Vault interface {
	secret.Vault
	io.Closer
}
