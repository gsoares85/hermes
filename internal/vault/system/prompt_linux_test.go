//go:build linux

package system

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/gsoares85/hermes/internal/core/secret"
)

const dialog = dbus.ObjectPath("/org/freedesktop/secrets/prompt/p1")

// The bug this branch fixed, and which nothing covered: closing the bus
// connection closes the signal channel, and a closed channel answers
// immediately and for ever. Without reading the second value the loop became a
// spin consuming a core until a context with no deadline of its own happened to
// be cancelled — which is what quitting Hermes with a dialog on screen did.
//
// The keychain job cannot reach this: it unlocks the keyring with an empty
// password, so it never raises a dialog.
func TestTheDialogGivesUpWhenTheBusCloses(t *testing.T) {
	t.Parallel()

	completed := make(chan *dbus.Signal)
	close(completed)

	answered := make(chan error, 1)
	go func() {
		answered <- awaitCompletion(t.Context(), dialog, completed, func() {
			t.Error("the dialog was dismissed on a bus that has closed")
		})
	}()

	select {
	case err := <-answered:
		if !errors.Is(err, secret.ErrUnavailable) {
			t.Errorf("awaitCompletion(...) = %v, want it to report the store as unavailable", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitCompletion never returned: a closed channel is being read for ever")
	}
}

// Giving up on the person takes the dialog off the screen. Leaving it there
// asks for a password nothing is waiting for any more.
func TestGivingUpOnTheDialogTakesItOffTheScreen(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	dismissed := 0
	err := awaitCompletion(ctx, dialog, make(chan *dbus.Signal), func() { dismissed++ })

	if !errors.Is(err, context.Canceled) {
		t.Errorf("awaitCompletion(...) = %v, want the cancellation", err)
	}
	if dismissed != 1 {
		t.Errorf("the dialog was dismissed %d times, want exactly 1", dismissed)
	}
}

// The tidy-up runs after the deadline that carried the operation has expired,
// so it cannot inherit it: a cancelled context would refuse the call outright
// and leave the dialog on screen. It gets a short deadline of its own, because
// the agent that has to answer is the one already suspected of being stuck —
// a goroutine wedged inside the code that guarantees cancellation is the defect
// this whole path exists to prevent.
func TestTheTidyUpHasADeadlineOfItsOwn(t *testing.T) {
	t.Parallel()

	ctx, cancel := dismissing()
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("the tidy-up runs with no deadline: it can wedge for ever")
	}
	if left := time.Until(deadline); left <= 0 || left > dismissTimeout {
		t.Errorf("the tidy-up has %v left, want at most %v", left, dismissTimeout)
	}
	if err := ctx.Err(); err != nil {
		t.Errorf("the tidy-up starts already cancelled: %v", err)
	}
}

// Signals for other objects, and the nil the bus can deliver, are skipped
// rather than mistaken for the answer.
func TestOnlyTheAnswerToThisDialogIsTheAnswer(t *testing.T) {
	t.Parallel()

	completed := make(chan *dbus.Signal, 4)
	completed <- nil
	completed <- &dbus.Signal{Path: "/somewhere/else", Name: promptInterface + ".Completed", Body: []any{true}}
	completed <- &dbus.Signal{Path: dialog, Name: promptInterface + ".Something", Body: []any{true}}
	completed <- &dbus.Signal{Path: dialog, Name: promptInterface + ".Completed", Body: []any{false}}

	if err := awaitCompletion(t.Context(), dialog, completed, func() {
		t.Error("the dialog was dismissed while it was still answering")
	}); err != nil {
		t.Errorf("awaitCompletion(...) = %v, want nil: the dialog was answered", err)
	}
}

// A dialog the person closed is a refusal, and has to be reported as one rather
// than as a password that is not there.
func TestADismissedDialogIsReported(t *testing.T) {
	t.Parallel()

	completed := make(chan *dbus.Signal, 1)
	completed <- &dbus.Signal{Path: dialog, Name: promptInterface + ".Completed", Body: []any{true}}

	err := awaitCompletion(t.Context(), dialog, completed, func() {})
	if !errors.Is(err, secret.ErrUnavailable) {
		t.Errorf("awaitCompletion(...) = %v, want it to report the store as unavailable", err)
	}
}
