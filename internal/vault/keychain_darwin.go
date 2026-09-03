//go:build darwin

package vault

import (
	"context"
	"errors"
	"fmt"

	"github.com/keybase/go-keychain"

	"github.com/gsoares85/hermes/internal/core/secret"
)

const (
	systemBackend = "the macOS keychain"
	systemAdvice  = "Unlock your login keychain in Keychain Access and start Hermes again."
)

// appleKeychain stores secrets as generic passwords in the default keychain.
//
// It talks to Security.framework through cgo rather than to the security
// command, and that is the whole reason this package does not use a library
// that covers all three systems: the popular one runs
// "security add-generic-password -w <password>", which puts the secret in the
// argument list of a child process. Adopting it would break the rule this task
// exists to enforce. See ADR-0010.
type appleKeychain struct{}

func openSystem(_ context.Context) (Vault, error) {
	// A missing item answers nil, nil here, so any error at all is the keychain
	// itself refusing: locked, absent, or unreachable from this process.
	if _, err := keychain.GetGenericPassword(probe.Service, probe.Account, "", ""); err != nil {
		return nil, fmt.Errorf("%w: %s did not answer: %w", secret.ErrUnavailable, systemBackend, err)
	}

	return appleKeychain{}, nil
}

func (appleKeychain) Get(ctx context.Context, ref secret.Ref) (string, error) {
	if err := secret.Usable(ctx, ref); err != nil {
		return "", err
	}

	data, err := keychain.GetGenericPassword(ref.Service, ref.Account, "", "")
	if err != nil {
		return "", fmt.Errorf("reading %v from %s: %w", ref, systemBackend, err)
	}
	if data == nil {
		return "", fmt.Errorf("%w: %v", secret.ErrNotFound, ref)
	}

	return string(data), nil
}

func (k appleKeychain) Set(ctx context.Context, ref secret.Ref, value string) error {
	if err := secret.Usable(ctx, ref); err != nil {
		return err
	}
	if value == "" {
		return fmt.Errorf("%w: %v", secret.ErrEmptySecret, ref)
	}

	item := keychain.NewGenericPassword(ref.Service, ref.Account, ref.String(), []byte(value), "")
	// Readable only while the keychain is unlocked, and never copied to iCloud:
	// a password to a production database has no business being replicated to
	// every device of the person who typed it.
	item.SetAccessible(keychain.AccessibleWhenUnlocked)
	item.SetSynchronizable(keychain.SynchronizableNo)

	err := keychain.AddItem(item)
	if errors.Is(err, keychain.ErrorDuplicateItem) {
		return k.replace(ref, value)
	}
	if err != nil {
		return fmt.Errorf("storing %v in %s: %w", ref, systemBackend, err)
	}

	return nil
}

// replace overwrites the secret of an item that is already there. The keychain
// refuses to add over an existing item rather than replacing it, so editing the
// password of a connection is an update, and doing it as delete-then-add would
// leave the connection with no password at all if the second call failed.
func (appleKeychain) replace(ref secret.Ref, value string) error {
	query := keychain.NewItem()
	query.SetSecClass(keychain.SecClassGenericPassword)
	query.SetService(ref.Service)
	query.SetAccount(ref.Account)

	update := keychain.NewItem()
	update.SetData([]byte(value))

	if err := keychain.UpdateItem(query, update); err != nil {
		return fmt.Errorf("replacing %v in %s: %w", ref, systemBackend, err)
	}

	return nil
}

func (appleKeychain) Delete(ctx context.Context, ref secret.Ref) error {
	if err := secret.Usable(ctx, ref); err != nil {
		return err
	}

	err := keychain.DeleteGenericPasswordItem(ref.Service, ref.Account)
	if errors.Is(err, keychain.ErrorItemNotFound) {
		return fmt.Errorf("%w: %v", secret.ErrNotFound, ref)
	}
	if err != nil {
		return fmt.Errorf("deleting %v from %s: %w", ref, systemBackend, err)
	}

	return nil
}

// Close releases nothing: Security.framework is reached by a call, not by a
// connection that has to be held.
func (appleKeychain) Close() error { return nil }
