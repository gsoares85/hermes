package vault

import (
	"context"
	"fmt"

	"github.com/gsoares85/hermes/internal/core/secret"
)

// Deferred is a vault that is still being opened.
//
// It exists because of a number: `docs/technical/TESTING.md` gives the cold
// start 1,5s, and opening the store of the operating system can take longer
// than that on its own — a Secret Service that has to be activated on the
// session bus, a login keychain that wants to be unlocked. Doing it before the
// window is built spends the whole budget on an answer that nothing on screen
// needs yet.
//
// So the opening starts and the window is drawn. Every method here waits for
// the answer, and none of them is on the path that draws the window: the first
// secret is read when a connection is opened, and the status is asked for by
// the window itself, asynchronously, after it is already on screen.
type Deferred struct {
	// Closed when the opening has finished. It is what publishes the two
	// fields below to every other goroutine.
	ready chan struct{}

	opened Vault
	status Status
}

// OpenInBackground begins opening the vault this machine can offer and returns
// at once.
//
// There is no error, for the same reason Open has none: a system with no usable
// store gets the in-memory vault and a warning that says so, and that is a
// thing to tell the person rather than a decision to hand back to the caller.
func OpenInBackground(ctx context.Context) *Deferred {
	return inBackground(func() (Vault, Status) { return Open(ctx) })
}

// inBackground is the seam the tests replace, so that the window between
// "started" and "answered" can be held open and looked at.
func inBackground(open func() (Vault, Status)) *Deferred {
	deferred := &Deferred{ready: make(chan struct{})}

	go func() {
		defer close(deferred.ready)

		deferred.opened, deferred.status = open()
	}()

	return deferred
}

// Status says which store ended up behind the vault, once that is settled.
func (d *Deferred) Status(ctx context.Context) (Status, error) {
	if err := d.wait(ctx); err != nil {
		return Status{}, err
	}

	return d.status, nil
}

// Get returns the secret stored under the reference.
func (d *Deferred) Get(ctx context.Context, ref secret.Ref) (string, error) {
	if err := d.wait(ctx); err != nil {
		return "", err
	}

	return d.opened.Get(ctx, ref)
}

// Set stores the secret, replacing whatever the reference held.
func (d *Deferred) Set(ctx context.Context, ref secret.Ref, value string) error {
	if err := d.wait(ctx); err != nil {
		return err
	}

	return d.opened.Set(ctx, ref, value)
}

// Delete removes the secret.
func (d *Deferred) Delete(ctx context.Context, ref secret.Ref) error {
	if err := d.wait(ctx); err != nil {
		return err
	}

	return d.opened.Delete(ctx, ref)
}

// Close releases whatever the platform held open.
//
// It waits for the opening rather than racing it, and takes no context while
// doing so: a bus connection that arrives after the application has decided to
// quit is a connection nothing else will ever have a reference to. The wait is
// bounded because Open is — it answers within its own detection timeout, always.
func (d *Deferred) Close() error {
	<-d.ready

	if err := d.opened.Close(); err != nil {
		return fmt.Errorf("closing the vault: %w", err)
	}

	return nil
}

func (d *Deferred) wait(ctx context.Context) error {
	select {
	case <-d.ready:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for the vault to open: %w", ctx.Err())
	}
}
