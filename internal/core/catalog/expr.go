package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
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
//
// # What pointing the path costs, and what pays for it
//
// Pointing the path at somebody else's schema means every unqualified name in
// every catalog query is resolved against a schema that somebody else can write
// to. That is CVE-2018-1058, and it is why every function these queries call is
// written as pg_catalog.something — including the two that look like syntax,
// pg_catalog.unnest and pg_catalog.array_agg.
//
// The trap is that pg_catalog is implicitly first on the path and that looks
// like protection. It is not. Path order only settles two candidates with
// identical signatures; it does not enter into it when the signatures differ,
// and there the ordinary type rules decide. The catalog's unnest takes anyarray
// and its array_agg takes anynonarray — both polymorphic — so a function
// declared for the exact type the query passes beats them from anywhere on the
// path. A schema owner who declares unnest(smallint[]) has the reader running
// their code with the connected role's rights, which on a DBA's connection is
// every right there is.
//
// Operators are qualified too, and the way that was arrived at is worth keeping
// because the shorter version of it was wrong.
//
// They were left bare at first, on the argument that pg_catalog has an exact
// operator for every comparison these queries make, so nothing could beat it.
// That was measured rather than assumed — two shadowing operators were declared
// and neither won — and the measurement was right. The conclusion was not. Those
// comparisons are safe because one side is an untyped literal, which the
// resolver assigns the other side's type; where both sides have a type and
// pg_catalog has no exact operator, it resolves by coercion, and a coercion
// loses to an exact match declared anywhere on the path. There is no exact
// =(oid, regclass) and no exact =(oid, integer), and this reads pg_depend and
// pg_constraint with both.
//
// So the argument had been turned into an invariant — "an exact operator always
// exists" — that nothing enforces. It was violated by the next query written
// after it was made. An invariant a codebase has to remember is not one.
//
// Everything is named now: every comparison operator, every IN turned into
// = ANY over an array so the operator can be named at all, and the CASE over
// classid made searched for the same reason. It is what pg_dump does, and this
// is why.
//
// Turning IN into = ANY changed what is being compared, and that is worth
// saying because the first version of this paragraph did not. IN ('r','p')
// compared against untyped literals, which the resolver gave the type of the
// column — an exact "char" against "char". ARRAY['r','p'] is text[], so the
// comparison is now a "char" against a text and resolves by coercion. Safe,
// because it is qualified; but it is a coercion where there was none, and it is
// the reason the corpus's shadowing ("char", text) operator has something to
// prove that it did not have before.
//
// What keeps this true is not the corpus, which only covers the pairs somebody
// thought of — the pair that went missing did so twice, and both times it was
// the one nobody had. It is TestNothingInAQueryResolvesByName, which reads these
// constants and refuses anything resolved by name, and
// TestEveryQuerySentIsOneThisTestChecked, which refuses a query the first one
// never saw.
//
// The first version of that check listed the forms it thought dangerous, which
// is a permit list, and a permit list needs its author to know every way
// PostgreSQL resolves a name. It missed !=, a=b without spaces, IN, LIKE and
// every function call — all confirmed hijackable — including the IN this code
// had just moved away from. It refuses rather than permits now, and the corpus
// stays because it proves the server behaves as this reasoning claims.

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
		// A context of its own, and that is the whole of this function.
		//
		// Putting the path back matters most exactly when the read did not
		// finish, and the commonest way for a read not to finish is the caller
		// cancelling it — which the product requires every long operation to
		// allow. Restoring through the caller's context would therefore fail
		// precisely in the case it exists for: the cancelled context refuses
		// the statement, the path stays pointing at the schema that was being
		// read, and the connection goes back to the pool resolving the next
		// caller's names in the wrong place.
		//
		// It still needs a deadline. Putting the path back is an ordinary
		// statement, and against a backend that stopped answering without
		// closing the socket it would wait for ever, holding the read that is
		// already failing. Same shape as the rollback a closing session runs.
		restore, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
		defer cancel()

		if _, err := r.setting(restore, searchPathBack, previous); err != nil {
			if aborted(err) {
				// The read failed and took the transaction with it, so the
				// server refuses every statement until somebody ends it —
				// including this one. Reporting that would put a second error
				// on top of every ordinary failure, and this one says the
				// connection is poisoned. The rollback that follows puts the
				// path back anyway, because a SET inside a transaction goes
				// back with it.
				return nil
			}

			return fmt.Errorf("putting the search path back to %q: %w", previous, err)
		}

		return nil
	}, nil
}

// abortedTransaction is what the server answers to anything at all once a
// statement in the transaction has failed.
const abortedTransaction = "25P02"

// aborted reports whether a failure is only the transaction refusing to go on.
//
// It is matched on the code rather than on the text because the text is
// localised and the code is not, and it is matched at all because the
// alternative is an alarm that rings on every ordinary failure. The alarm this
// is keeping quiet — a search path that did not go back — means a connection
// resolving the next caller's names in the wrong schema, so it has to mean that
// and nothing else.
func aborted(err error) bool {
	return strings.Contains(err.Error(), abortedTransaction)
}

// restoreTimeout bounds putting the search path back. Short, because it runs
// when a read has already failed and a caller is waiting to be told so.
const restoreTimeout = 5 * time.Second

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
