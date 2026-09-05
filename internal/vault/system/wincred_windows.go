//go:build windows

package system

import (
	"context"
	"errors"
	"fmt"

	"github.com/danieljoos/wincred"

	"github.com/gsoares85/hermes/internal/core/secret"
)

// Backend names the store to a person, and Advice says what to do when it does
// not answer. Both are prose: they end up in the warning the window shows, and
// somebody has to be able to act on them.
const (
	Backend = "the Windows Credential Manager"
	Advice  = "Check that the Credential Manager service is running, then start Hermes again."
)

// credentialManager stores secrets as generic credentials of the current user.
//
// Windows encrypts the blob with DPAPI under the login credentials of the user,
// so what reaches the disk is unreadable to every other account on the machine
// and to the machine itself once the account is gone. Nothing here spawns a
// process: CredRead and CredWrite are calls, and a password that never becomes
// an argument never reaches a command line.
type credentialManager struct{}

// Open answers the Credential Manager of this user, and refuses when it cannot
// be reached.
//
// The read of the probe is the availability check: a store that is there
// answers "element not found", and one that is not answers something else.
func Open(_ context.Context) (Vault, error) {
	if _, err := wincred.GetGenericCredential(target(probe)); err != nil &&
		!errors.Is(err, wincred.ErrElementNotFound) {
		return nil, fmt.Errorf("%w: %s did not answer: %w", secret.ErrUnavailable, Backend, err)
	}

	return credentialManager{}, nil
}

func (credentialManager) Get(ctx context.Context, ref secret.Ref) (string, error) {
	if err := secret.Usable(ctx, ref); err != nil {
		return "", err
	}

	credential, err := wincred.GetGenericCredential(target(ref))
	if errors.Is(err, wincred.ErrElementNotFound) {
		return "", fmt.Errorf("%w: %v", secret.ErrNotFound, ref)
	}
	if err != nil {
		return "", fmt.Errorf("reading %v from %s: %w", ref, Backend, err)
	}

	return string(credential.CredentialBlob), nil
}

func (credentialManager) Set(ctx context.Context, ref secret.Ref, value string) error {
	if err := secret.Usable(ctx, ref); err != nil {
		return err
	}
	if value == "" {
		return fmt.Errorf("%w: %v", secret.ErrEmptySecret, ref)
	}

	credential := wincred.NewGenericCredential(target(ref))
	credential.CredentialBlob = []byte(value)
	// Shown beside the entry in the Credential Manager window. Someone auditing
	// what an application stored should be able to tell which connection an
	// entry belongs to without opening it.
	credential.UserName = ref.Account
	credential.Persist = wincred.PersistLocalMachine

	if err := credential.Write(); err != nil {
		return fmt.Errorf("storing %v in %s: %w", ref, Backend, err)
	}

	return nil
}

func (credentialManager) Delete(ctx context.Context, ref secret.Ref) error {
	if err := secret.Usable(ctx, ref); err != nil {
		return err
	}

	err := wincred.NewGenericCredential(target(ref)).Delete()
	if errors.Is(err, wincred.ErrElementNotFound) {
		return fmt.Errorf("%w: %v", secret.ErrNotFound, ref)
	}
	if err != nil {
		return fmt.Errorf("deleting %v from %s: %w", ref, Backend, err)
	}

	return nil
}

// Close releases nothing: the Credential Manager is reached by a call, not by a
// connection that has to be held.
func (credentialManager) Close() error { return nil }

// target is the name one secret is filed under. The Credential Manager has a
// single flat namespace shared by everything the person runs, so the service
// belongs in the name — which is exactly what the rendering of a reference
// already is.
func target(ref secret.Ref) string { return ref.String() }
