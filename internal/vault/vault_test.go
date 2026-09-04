package vault

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/core/secret"
	"github.com/gsoares85/hermes/internal/core/secret/secrettest"
)

// The tests live inside the package because what they exercise is the choice
// between a store and the fallback, and that choice is only observable with a
// double in place of the store of the operating system. Driving it through the
// real keychain instead would test the keychain, on one platform, and leave the
// decision itself untested on the other two.

func TestOpenUsesTheStoreOfTheSystemWhenItAnswers(t *testing.T) {
	t.Parallel()

	system := newStub(0)

	vault, status := open(t.Context(), system.open, time.Second)
	t.Cleanup(func() { _ = vault.Close() })

	if status.Backend != systemBackend {
		t.Errorf("backend is %q, want %q", status.Backend, systemBackend)
	}
	if status.Warning != "" {
		t.Errorf("an available vault carries the warning %q, want none", status.Warning)
	}
}

func TestOpenFallsBackToMemoryWhenTheSystemHasNoStore(t *testing.T) {
	t.Parallel()

	refused := failing(errors.New("no keyring is running"))

	vault, status := open(t.Context(), refused, time.Second)
	t.Cleanup(func() { _ = vault.Close() })

	if status.Backend != BackendMemory {
		t.Errorf("backend is %q, want %q", status.Backend, BackendMemory)
	}
	if status.Warning == "" {
		t.Fatal("falling back to memory carries no warning: the failure would be silent")
	}
}

// The warning is the whole difference between a fallback and a silent failure.
// It has to say what went wrong, that the passwords now live only in this
// session, and what to install to get them remembered again.
func TestTheWarningSaysWhatBrokeAndWhatToDoAboutIt(t *testing.T) {
	t.Parallel()

	vault, status := open(t.Context(), failing(errors.New("no keyring is running")), time.Second)
	t.Cleanup(func() { _ = vault.Close() })

	for _, want := range []string{"no keyring is running", systemBackend, systemAdvice, "this session"} {
		if !strings.Contains(status.Warning, want) {
			t.Errorf("the warning does not mention %q:\n%s", want, status.Warning)
		}
	}
}

// A keychain can hang rather than fail: on Linux it is a round trip on a bus
// that may have no one on the other end, and on macOS it can be a dialog. The
// window must not wait for it, so the detection has a deadline of its own and
// the fallback is what a late answer produces.
func TestOpenFallsBackWhenTheSystemStoreDoesNotAnswerInTime(t *testing.T) {
	t.Parallel()

	slow := func(ctx context.Context) (Vault, error) {
		<-ctx.Done()

		return nil, ctx.Err()
	}

	start := time.Now()
	vault, status := open(t.Context(), slow, 20*time.Millisecond)
	t.Cleanup(func() { _ = vault.Close() })

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the detection took %v: the deadline did not apply", elapsed)
	}
	if status.Backend != BackendMemory {
		t.Errorf("backend is %q, want %q", status.Backend, BackendMemory)
	}
	if status.Warning == "" {
		t.Error("a vault that timed out carries no warning")
	}
}

// The answer that arrives after the deadline still holds a bus connection, and
// nothing else will ever have a reference to it. Dropping it on the floor would
// leak a socket per start of the application.
func TestOpenClosesAStoreThatAnswersTooLate(t *testing.T) {
	t.Parallel()

	late := newStub(30 * time.Millisecond)

	vault, _ := open(t.Context(), late.open, time.Millisecond)
	t.Cleanup(func() { _ = vault.Close() })

	select {
	case <-late.closed:
	case <-time.After(2 * time.Second):
		t.Error("the late answer was never closed: its connection leaks")
	}
}

// The fallback is not a stub that reports success: it is the vault of everyone
// without a keychain, so it answers the whole contract. Running the same suite
// the in-memory vault passes is what keeps that true as this package grows.
func TestTheFallbackHonoursTheWholeContract(t *testing.T) {
	t.Parallel()

	secrettest.Run(t, func(t *testing.T) secret.Vault {
		t.Helper()

		vault, _ := open(t.Context(), failing(errors.New("no keyring is running")), time.Second)
		t.Cleanup(func() { _ = vault.Close() })

		return vault
	})
}

// stub stands in for the store of an operating system, and records that it was
// closed.
type stub struct {
	vault  secret.Vault
	delay  time.Duration
	closed chan struct{}
}

func newStub(delay time.Duration) *stub {
	return &stub{vault: secret.NewMemory(), delay: delay, closed: make(chan struct{})}
}

// The context is deliberately ignored. This stub exists to be the store that
// answers too late, and one that gave up the moment the deadline passed would
// answer at that same instant — leaving the select in open with both cases
// ready and the choice to the runtime, which is a test that fails once in a
// while for a reason nobody can reproduce.
func (s *stub) open(context.Context) (Vault, error) {
	time.Sleep(s.delay)

	return s, nil
}

func (s *stub) Get(ctx context.Context, ref secret.Ref) (string, error) {
	return s.vault.Get(ctx, ref)
}

func (s *stub) Set(ctx context.Context, ref secret.Ref, value string) error {
	return s.vault.Set(ctx, ref, value)
}

func (s *stub) Delete(ctx context.Context, ref secret.Ref) error {
	return s.vault.Delete(ctx, ref)
}

func (s *stub) Close() error {
	close(s.closed)

	return nil
}

func failing(reason error) opener {
	return func(context.Context) (Vault, error) { return nil, reason }
}
