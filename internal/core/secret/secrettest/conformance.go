// Package secrettest holds the conformance suite every vault must pass.
//
// It exists because of what the in-memory vault is: at the same time the double
// the core is tested with and the real vault of anyone whose system has no
// keychain. A double that is more forgiving than the real thing is worse than no
// double at all — the core would be proven against a fiction, and the difference
// would surface on a user's machine instead of in the pipeline. One suite, run
// here against the in-memory vault and in internal/vault against each keychain,
// is what keeps the two the same.
//
// The suite never assumes an empty store. A keychain is shared by the whole
// login session, so every reference it creates carries a unique account and is
// deleted afterwards, leaving nothing behind on the machine that ran it.
package secrettest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/secret"
)

// Open builds a vault to run the suite against. It is called once per case, so
// an implementation backed by a real keychain may return the same store every
// time: the cases are isolated by their references, not by their store.
type Open func(t *testing.T) secret.Vault

// Run executes the whole contract against an implementation.
func Run(t *testing.T, open Open) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, vault secret.Vault)
	}{
		{"a stored secret comes back", storedSecretComesBack},
		{"an unknown reference is not found", unknownReferenceIsNotFound},
		{"storing twice keeps the last value", storingTwiceKeepsTheLastValue},
		{"a deleted secret is gone", deletedSecretIsGone},
		{"deleting an unknown reference is not found", deletingUnknownReferenceIsNotFound},
		{"references do not collide", referencesDoNotCollide},
		{"an empty secret is refused", emptySecretIsRefused},
		{"an invalid reference is refused", invalidReferenceIsRefused},
		{"a cancelled context stores nothing", cancelledContextStoresNothing},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.run(t, open(t))
		})
	}
}

func storedSecretComesBack(t *testing.T, vault secret.Vault) {
	ref := reference(t, vault)

	if err := vault.Set(t.Context(), ref, "hunter2"); err != nil {
		t.Fatalf("storing the secret: %v", err)
	}

	got, err := vault.Get(t.Context(), ref)
	if err != nil {
		t.Fatalf("reading the secret back: %v", err)
	}
	if got != "hunter2" {
		t.Errorf("read %q, want the value that was stored", got)
	}
}

func unknownReferenceIsNotFound(t *testing.T, vault secret.Vault) {
	got, err := vault.Get(t.Context(), reference(t, vault))

	if !errors.Is(err, secret.ErrNotFound) {
		t.Errorf("reading an unknown reference returned %v, want ErrNotFound", err)
	}
	if got != "" {
		t.Errorf("reading an unknown reference returned %q, want the empty string", got)
	}
}

func storingTwiceKeepsTheLastValue(t *testing.T, vault secret.Vault) {
	ref := reference(t, vault)

	if err := vault.Set(t.Context(), ref, "first"); err != nil {
		t.Fatalf("storing the first value: %v", err)
	}
	if err := vault.Set(t.Context(), ref, "second"); err != nil {
		t.Fatalf("storing over the first value: %v", err)
	}

	got, err := vault.Get(t.Context(), ref)
	if err != nil {
		t.Fatalf("reading the secret back: %v", err)
	}
	if got != "second" {
		t.Errorf("read %q, want the value stored last: editing a password must replace it", got)
	}
}

func deletedSecretIsGone(t *testing.T, vault secret.Vault) {
	ref := reference(t, vault)

	if err := vault.Set(t.Context(), ref, "hunter2"); err != nil {
		t.Fatalf("storing the secret: %v", err)
	}
	if err := vault.Delete(t.Context(), ref); err != nil {
		t.Fatalf("deleting the secret: %v", err)
	}

	if _, err := vault.Get(t.Context(), ref); !errors.Is(err, secret.ErrNotFound) {
		t.Errorf("reading a deleted secret returned %v, want ErrNotFound", err)
	}
}

// Deleting something that is not there answers ErrNotFound rather than nothing
// at all. Removing a connection whose secret has already gone is a normal
// outcome and the caller ignores that error on purpose; a reference computed
// wrongly is a bug, and silence would hide it behind a successful delete.
func deletingUnknownReferenceIsNotFound(t *testing.T, vault secret.Vault) {
	if err := vault.Delete(t.Context(), reference(t, vault)); !errors.Is(err, secret.ErrNotFound) {
		t.Errorf("deleting an unknown reference returned %v, want ErrNotFound", err)
	}
}

// Two connections are two references, and one must never answer for the other.
// This is the property that makes the account a stable identifier rather than
// the name of a connection, which the user can change and repeat.
func referencesDoNotCollide(t *testing.T, vault secret.Vault) {
	first, second := reference(t, vault), reference(t, vault)

	if err := vault.Set(t.Context(), first, "first"); err != nil {
		t.Fatalf("storing the first secret: %v", err)
	}
	if err := vault.Set(t.Context(), second, "second"); err != nil {
		t.Fatalf("storing the second secret: %v", err)
	}
	if err := vault.Delete(t.Context(), first); err != nil {
		t.Fatalf("deleting the first secret: %v", err)
	}

	got, err := vault.Get(t.Context(), second)
	if err != nil {
		t.Fatalf("reading the second secret: %v", err)
	}
	if got != "second" {
		t.Errorf("the second secret reads %q: one reference answered for another", got)
	}
}

// A connection with no password is a connection that stores no secret, not one
// that stores an empty one. Accepting the empty string would report success and
// read back a value that fails to authenticate for a reason nobody can see.
func emptySecretIsRefused(t *testing.T, vault secret.Vault) {
	ref := reference(t, vault)

	if err := vault.Set(t.Context(), ref, ""); !errors.Is(err, secret.ErrEmptySecret) {
		t.Errorf("storing an empty secret returned %v, want ErrEmptySecret", err)
	}
	if _, err := vault.Get(t.Context(), ref); !errors.Is(err, secret.ErrNotFound) {
		t.Errorf("a refused Set left something behind: Get returned %v, want ErrNotFound", err)
	}
}

func invalidReferenceIsRefused(t *testing.T, vault secret.Vault) {
	invalid := secret.Ref{Service: secret.Service}

	if _, err := vault.Get(t.Context(), invalid); !errors.Is(err, secret.ErrInvalidRef) {
		t.Errorf("Get with an invalid reference returned %v, want ErrInvalidRef", err)
	}
	if err := vault.Set(t.Context(), invalid, "hunter2"); !errors.Is(err, secret.ErrInvalidRef) {
		t.Errorf("Set with an invalid reference returned %v, want ErrInvalidRef", err)
	}
	if err := vault.Delete(t.Context(), invalid); !errors.Is(err, secret.ErrInvalidRef) {
		t.Errorf("Delete with an invalid reference returned %v, want ErrInvalidRef", err)
	}
}

// Every call takes a context because reading a keychain can open a dialog and
// wait for a person. An implementation that ignores it would hang the window,
// so the contract requires the check even where the store is a map.
func cancelledContextStoresNothing(t *testing.T, vault secret.Vault) {
	ref := reference(t, vault)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := vault.Set(ctx, ref, "hunter2"); !errors.Is(err, context.Canceled) {
		t.Errorf("Set on a cancelled context returned %v, want context.Canceled", err)
	}
	if _, err := vault.Get(ctx, ref); !errors.Is(err, context.Canceled) {
		t.Errorf("Get on a cancelled context returned %v, want context.Canceled", err)
	}
	if err := vault.Delete(ctx, ref); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete on a cancelled context returned %v, want context.Canceled", err)
	}

	if _, err := vault.Get(t.Context(), ref); !errors.Is(err, secret.ErrNotFound) {
		t.Errorf("the cancelled Set stored something: Get returned %v, want ErrNotFound", err)
	}
}

// reference returns an account nothing else uses, and removes whatever the case
// stored under it. A keychain outlives the test binary: without this, a run
// would leave items behind on the machine and the next one would read them.
func reference(t *testing.T, vault secret.Vault) secret.Ref {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("generating a unique account: %v", err)
	}

	ref := secret.ConnectionRef(strings.ReplaceAll(t.Name(), "/", ".") + "-" + hex.EncodeToString(suffix))

	t.Cleanup(func() {
		if err := vault.Delete(context.WithoutCancel(t.Context()), ref); err != nil &&
			!errors.Is(err, secret.ErrNotFound) {
			t.Errorf("cleaning up %v: %v", ref, err)
		}
	})

	return ref
}
