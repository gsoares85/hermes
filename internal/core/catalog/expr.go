package catalog

import (
	"context"
	"errors"
	"fmt"
)

// The canonical form of an expression is asked of the server, not computed here.
//
// Everything this model keeps as text is rendered by the server from the parse
// tree: the default of a column, the expression of a check, the WHERE of a
// partial index, the body of a view. That rendering has one input the reader
// controls, and controlling it is the whole of this file — search_path.
//
// A name the server cannot find on the path is written with its schema. So the
// same table read under two names answers
// FOREIGN KEY (order_id) REFERENCES staging.orders(id) in one and
// REFERENCES production.orders(id) in the other, and nextval('staging.x_seq')
// against nextval('production.x_seq'), and a view whose every FROM names the
// schema it lives in. Comparing an environment against its copy is exactly that
// pair of readings, and every one of those differences is the diff reporting a
// change nobody made — on every foreign key, every serial column and every view
// at once.
//
// So the reader points the path at the schema it is reading, for as long as it
// reads, and puts it back. It is what pg_dump does and for the same reason: the
// server knows which names it can leave out, and it is the only thing that
// does. Cutting the schema out of the rendered text here cannot be made safe —
// telling a qualification from a string literal that happens to contain a dot
// is a question about the query rather than about its characters, and a reader
// that guesses wrong corrupts a default instead of normalising it.
//
// The path is session state the reader does not own, which is why it is put
// back rather than left pointing somewhere convenient, and why failing to put
// it back is reported instead of swallowed: a connection returned to the pool
// with someone else's search path resolves the next caller's names against the
// wrong schema.
//
// What this does not fix is the one thing the server itself changed: from
// PostgreSQL 16 a column inside a view is no longer written with the relation
// it came from when nothing is ambiguous. That is a difference in what the two
// servers store, not in what the path resolves, and no rewriting here can undo
// it without making two different views compare equal. See ADR-0012.

// searchPathNow asks what the path is before the reader changes it, so that
// what goes back is what was there rather than a default this package invented.
const searchPathNow = `SELECT pg_catalog.current_setting('search_path')`

// searchPathOfSchema points the path at one schema.
//
// quote_ident is the server's own quoting, and it is used because the value of
// search_path is a list of identifiers rather than a string: a schema called
// My Sales has to arrive as "My Sales" or the path silently names two schemas
// that do not exist. Quoting it here, in Go, would be this package inventing a
// second implementation of a rule the server already has.
const searchPathOfSchema = `SELECT pg_catalog.set_config('search_path', pg_catalog.quote_ident($1), false)`

// searchPathBack restores a value that came from the session, so it goes back
// exactly as it came and is not re-quoted on the way.
const searchPathBack = `SELECT pg_catalog.set_config('search_path', $1, false)`

// scopeTo points the session's search path at the schema and answers how to put
// it back.
func (r *Reader) scopeTo(ctx context.Context, schema Name) (func() error, error) {
	previous, err := r.setting(ctx, searchPathNow)
	if err != nil {
		return nil, fmt.Errorf("reading the search path: %w", err)
	}

	if _, err := r.setting(ctx, searchPathOfSchema, schema.String()); err != nil {
		return nil, fmt.Errorf("pointing the search path at %s: %w", schema, err)
	}

	return func() error {
		if _, err := r.setting(ctx, searchPathBack, previous); err != nil {
			return fmt.Errorf("putting the search path back to %q: %w", previous, err)
		}

		return nil
	}, nil
}

// setting runs one of the search path queries and answers the value it left.
//
// A missing row is a failure rather than an empty string, because the empty
// string is a search path — one that finds nothing — and restoring it would
// leave the connection unable to resolve an unqualified name at all.
func (r *Reader) setting(ctx context.Context, sql string, args ...any) (string, error) {
	rows := r.server.Query(ctx, sql, args...)
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", err
		}

		return "", errors.New("the server answered no row")
	}

	var value string
	if err := rows.Scan(&value); err != nil {
		return "", err
	}

	if err := rows.Err(); err != nil {
		return "", err
	}

	return value, nil
}
