package vault

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/core/secret"
)

// opening is a vault whose opening the test controls, so that the window
// between "the application started" and "the operating system answered" can be
// held open and looked at.
func opening(release <-chan struct{}, answer Vault, status Status) func() (Vault, Status) {
	return func() (Vault, Status) {
		<-release

		return answer, status
	}
}

// The property the startup budget depends on: beginning to open the vault
// returns at once, however long the operating system takes to answer.
func TestOpenInBackgroundReturnsBeforeTheVaultIsOpen(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	deferred := inBackground(opening(release, session{secret.NewMemory()}, Status{Backend: BackendMemory}))
	defer func() { close(release); _ = deferred.Close() }()

	// Nothing to assert about the clock: reaching this line at all is the
	// property, because the opener above cannot have finished.
	select {
	case <-deferred.ready:
		t.Fatal("OpenInBackground waited for the vault to open")
	default:
	}
}

// Once the answer exists, it is the answer the caller gets — the deferral is
// about when, never about what.
func TestTheDeferredVaultAnswersWhatTheOpeningProduced(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	want := Status{Backend: "the keychain of somewhere", Warning: "install a keyring"}
	deferred := inBackground(opening(release, session{secret.NewMemory()}, want))
	defer func() { _ = deferred.Close() }()

	close(release)

	got, err := deferred.Status(t.Context())
	if err != nil {
		t.Fatalf("Status() = %v", err)
	}
	if got != want {
		t.Errorf("Status() = %+v, want %+v", got, want)
	}
}

// Every method waits for the opening and then does what it was asked, so a
// caller never has to know whether the vault was ready when it called.
func TestTheDeferredVaultCarriesTheContractThrough(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	deferred := inBackground(opening(release, session{secret.NewMemory()}, Status{}))
	defer func() { _ = deferred.Close() }()

	close(release)

	ref := secret.Ref{Service: secret.Service, Account: "deferred"}

	if err := deferred.Set(t.Context(), ref, "s3cr3t"); err != nil {
		t.Fatalf("Set(...) = %v", err)
	}

	stored, err := deferred.Get(t.Context(), ref)
	if err != nil {
		t.Fatalf("Get(...) = %v", err)
	}
	if stored != "s3cr3t" {
		t.Errorf("Get(...) = %q, want the secret that was stored", stored)
	}

	if err := deferred.Delete(t.Context(), ref); err != nil {
		t.Errorf("Delete(...) = %v", err)
	}
	if _, err := deferred.Get(t.Context(), ref); !errors.Is(err, secret.ErrNotFound) {
		t.Errorf("Get(...) after Delete = %v, want ErrNotFound", err)
	}
}

// A window that gave up must not be left holding a call that cannot finish.
// This is the whole reason every method takes a context.
func TestTheDeferredVaultGivesUpWithTheCaller(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	deferred := inBackground(opening(release, session{secret.NewMemory()}, Status{}))
	defer func() { close(release); _ = deferred.Close() }()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := deferred.Status(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Status() on a cancelled context = %v, want context.Canceled", err)
	}
	if _, err := deferred.Get(ctx, secret.Ref{Service: secret.Service, Account: "a"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Get() on a cancelled context = %v, want context.Canceled", err)
	}
	if err := deferred.Set(ctx, secret.Ref{Service: secret.Service, Account: "a"}, "x"); !errors.Is(err, context.Canceled) {
		t.Errorf("Set() on a cancelled context = %v, want context.Canceled", err)
	}
	if err := deferred.Delete(ctx, secret.Ref{Service: secret.Service, Account: "a"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete() on a cancelled context = %v, want context.Canceled", err)
	}
}

// Closing while the opening is still running has to close what the opening
// produces, not race past it: a bus connection that arrives after the
// application decided to quit is one nothing will ever close.
func TestClosingADeferredVaultWaitsForWhatItOpened(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	closed := make(chan struct{})
	deferred := inBackground(opening(release, closes(closed), Status{}))

	finished := make(chan error, 1)

	go func() { finished <- deferred.Close() }()

	select {
	case <-closed:
		t.Fatal("Close() closed a vault that had not been opened yet")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)

	if err := <-finished; err != nil {
		t.Errorf("Close() = %v", err)
	}
	select {
	case <-closed:
	default:
		t.Error("Close() returned without closing the vault the opening produced")
	}
}

// closes is a vault that records having been closed.
type closes chan struct{}

func (c closes) Get(context.Context, secret.Ref) (string, error) { return "", secret.ErrNotFound }
func (c closes) Set(context.Context, secret.Ref, string) error   { return nil }
func (c closes) Delete(context.Context, secret.Ref) error        { return nil }
func (c closes) Close() error                                    { close(c); return nil }
