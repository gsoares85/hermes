package postgres

import (
	"errors"
	"testing"

	"github.com/gsoares85/hermes/internal/driver"
)

// A closed session has released its connection and nils the field that held it.
// Nothing here checks that field, so the pooled connection reaches a statement
// as a nil pointer inside a non-nil interface — and the pgx type does not guard
// its own receiver. The result is a panic that takes the application down
// rather than a statement that fails.
//
// The state is built by closing, not by hand: what is asserted is the state
// Close actually leaves behind.
func TestAClosedSessionRefusesWorkInsteadOfPanicking(t *testing.T) {
	t.Parallel()

	closed := func(t *testing.T) *session {
		t.Helper()

		s := &session{}
		s.Close()

		return s
	}

	t.Run("Exec", func(t *testing.T) {
		t.Parallel()

		if err := closed(t).Exec(t.Context(), "SELECT 1"); !errors.Is(err, driver.ErrSessionClosed) {
			t.Errorf("Exec on a closed session = %v, want ErrSessionClosed", err)
		}
	})

	t.Run("QueryRow", func(t *testing.T) {
		t.Parallel()

		var value int
		err := closed(t).QueryRow(t.Context(), "SELECT 1").Scan(&value)
		if !errors.Is(err, driver.ErrSessionClosed) {
			t.Errorf("Scan after QueryRow on a closed session = %v, want ErrSessionClosed", err)
		}
	})

	t.Run("Begin", func(t *testing.T) {
		t.Parallel()

		if err := closed(t).Begin(t.Context()); !errors.Is(err, driver.ErrSessionClosed) {
			t.Errorf("Begin on a closed session = %v, want ErrSessionClosed", err)
		}
	})
}

// Close is documented as safe to call more than once, and the second call must
// not be the one that panics.
func TestClosingTwiceIsSafe(t *testing.T) {
	t.Parallel()

	s := &session{}
	s.Close()
	s.Close()
}
