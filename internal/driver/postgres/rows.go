package postgres

import (
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/gsoares85/hermes/internal/driver"
)

// row is a single pgx row seen through the seam.
//
// It is not here to seal a leak, and it is worth being exact about that: what
// pgx returns from QueryRow is a defined type over its result set, so it gets a
// fresh method set and carries only Scan. Nothing extra crosses without it.
//
// It is here so that a failure on this path reads like a failure on the other
// one. rows.Scan says which operation failed; this said nothing, so the same
// mistake produced a message with context through Query and a bare pgx error
// through QueryRow. And it makes the adapter uniform, which is what stops the
// day pgx gives that type another method from being a day nobody notices.
type row struct{ inner pgx.Row }

// Scan is not classified, for the reason given on rows.Scan below: a failure
// here is a mismatch between the query and what the caller asked to read it
// into, which is a bug in this repository rather than a condition of the
// server.
func (r row) Scan(dest ...any) error {
	if err := r.inner.Scan(dest...); err != nil {
		return fmt.Errorf("reading the row: %w", err)
	}

	return nil
}

// rows is a pgx result set seen through the seam.
//
// It holds the pgx value in a field rather than embedding it, and that is the
// whole point of the type. Embedding would promote every method pgx.Rows has —
// Conn, RawValues, FieldDescriptions — so the value handed back as driver.Rows
// could be asserted straight back to pgx.Rows, and the seam ADR-0009 draws
// would be a suggestion. Four methods go through; nothing else does.
//
// It also earns its keep beyond sealing the type: Err is where a broken
// connection surfaces on this path, and the layer above has to tell a server
// that went away from a query that was wrong. classify is what draws that
// difference, and it can only be applied where the error comes out.
type rows struct{ inner pgx.Rows }

func (r rows) Next() bool { return r.inner.Next() }

// Scan is not classified. A failure here is a mismatch between the query and
// what the caller asked to read it into — a bug in this repository, not a
// condition of the server — and dressing it as an engine failure would send the
// reader looking at their network.
func (r rows) Scan(dest ...any) error {
	if err := r.inner.Scan(dest...); err != nil {
		return fmt.Errorf("reading a row: %w", err)
	}

	return nil
}

func (r rows) Err() error {
	if err := r.inner.Err(); err != nil {
		return classify(fmt.Errorf("reading the result: %w", err))
	}

	return nil
}

func (r rows) Close() { r.inner.Close() }

// failedRows carries a failure that was found before the query was sent, in the
// shape a result set travels in.
//
// It is the counterpart of failedRow, and it answers on all three paths a
// caller might take: the loop ends immediately, Err says what happened, and so
// does Scan for anyone who reached for a row without checking the loop. A type
// that only answered Err would let a caller that ignores it read the failure as
// an empty result — which for a catalog read is a schema that looks empty.
type failedRows struct{ err error }

func (r failedRows) Next() bool        { return false }
func (r failedRows) Scan(...any) error { return r.err }
func (r failedRows) Err() error        { return r.err }
func (r failedRows) Close()            {}

var _ driver.Rows = rows{}
var _ driver.Rows = failedRows{}
