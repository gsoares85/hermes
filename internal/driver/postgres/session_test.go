package postgres

import (
	"errors"
	"strings"
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
//
// There is no equivalent for a single row, and the reason is worth writing down
// so nobody adds one. pgx.Row and driver.Row are the same interface written
// twice — one method, Scan — so asserting either to the other always succeeds
// and proves nothing, and what pgx returns from QueryRow is a defined type over
// its result set, which gets a fresh method set carrying only Scan. Nothing to
// seal, and a test would pass whatever the adapter did.
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

// A failure reading a single row says which operation failed, like a failure
// reading a result set does.
//
// It is the whole reason the single row has a wrapper. Nothing leaks without
// one — what pgx returns from QueryRow carries only Scan — but the same mistake
// produced a message with context through Query and a bare pgx error through
// QueryRow, and the layer above cannot tell where a message with no operation
// in it came from.
func TestAFailureReadingARowSaysWhatFailed(t *testing.T) {
	t.Parallel()

	broken := errors.New("no rows in result set")

	err := row{inner: scanFails{err: broken}}.Scan(new(int))
	if !errors.Is(err, broken) {
		t.Fatalf("Scan() = %v, want the failure underneath", err)
	}
	if !strings.Contains(err.Error(), "reading the row") {
		t.Errorf("Scan() = %q, want it to say which operation failed", err)
	}
}

// scanFails is a pgx row that only ever fails.
type scanFails struct{ err error }

func (s scanFails) Scan(...any) error { return s.err }
