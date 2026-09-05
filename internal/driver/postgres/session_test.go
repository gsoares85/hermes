package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

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

	// A result set has two places a failure can surface — the loop and Err —
	// and a caller that only reads the loop would take an empty answer for an
	// empty table. Both have to say the session is closed.
	t.Run("Query", func(t *testing.T) {
		t.Parallel()

		rows := closed(t).Query(t.Context(), "SELECT 1")
		defer rows.Close()

		if rows.Next() {
			t.Error("Next on a closed session answered true, so the loop would run")
		}
		if err := rows.Err(); !errors.Is(err, driver.ErrSessionClosed) {
			t.Errorf("Err after Query on a closed session = %v, want ErrSessionClosed", err)
		}
		if err := rows.Scan(); !errors.Is(err, driver.ErrSessionClosed) {
			t.Errorf("Scan after Query on a closed session = %v, want ErrSessionClosed", err)
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

// The seam is a seal, not a label.
//
// Embedding pgx.Rows in the adapter would promote every method it has, so the
// value handed back as driver.Rows could be asserted straight back to the pgx
// interface — and the core layer would reach Conn(), FieldDescriptions() and
// the rest through a type it is not allowed to import. That is the arrangement
// ADR-0009 exists to prevent, and an interface it can be asserted through is
// not a seam.
func TestAResultSetDoesNotCarryThePgxTypeAcrossTheSeam(t *testing.T) {
	t.Parallel()

	crossing := map[string]driver.Rows{
		"a result":        rows{},
		"a failed result": failedRows{},
	}

	for name, result := range crossing {
		if _, isPgx := result.(pgx.Rows); isPgx {
			t.Errorf("%s can be asserted back to pgx.Rows, so the seam is a label", name)
		}
	}
}
