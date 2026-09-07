package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gsoares85/hermes/internal/driver"
)

// rollbackTimeout bounds the rollback a closing session performs. Short on
// purpose: the connection is being given up either way, and the alternative to
// giving up on the rollback is holding the pool open indefinitely.
const rollbackTimeout = 5 * time.Second

// session is one connection checked out of the pool.
//
// It holds the connection until Close, which is what makes two sessions
// independent: each has its own backend, so a transaction open on one is
// invisible to the other until it commits.
type session struct {
	conn *pgxpool.Conn
	tx   pgx.Tx
}

func (p *connPool) Session(ctx context.Context) (driver.Session, error) {
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return nil, classify(fmt.Errorf("checking out a connection: %w", err))
	}

	return &session{conn: conn}, nil
}

// runner is what a statement goes through: the open transaction if there is
// one, the connection otherwise. Both pgx types already satisfy it.
type runner interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// runner picks between the two, once, so that no method below repeats the
// branch — and so that forgetting it cannot send a statement outside the
// transaction the caller opened.
//
// It returns nil for a closed session. The explicit check is what makes that
// possible: a nil *pgxpool.Conn returned as a runner would be a non-nil
// interface holding a nil pointer, which compares unequal to nil and panics on
// the first call instead of failing.
func (s *session) runner() runner {
	if s.tx != nil {
		return s.tx
	}

	if s.conn == nil {
		return nil
	}

	return s.conn
}

func (s *session) Exec(ctx context.Context, sql string, args ...any) error {
	run := s.runner()
	if run == nil {
		return driver.ErrSessionClosed
	}

	if _, err := run.Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("running the statement: %w", err)
	}

	return nil
}

func (s *session) QueryRow(ctx context.Context, sql string, args ...any) driver.Row {
	run := s.runner()
	if run == nil {
		return failedRow{err: driver.ErrSessionClosed}
	}

	return row{inner: run.QueryRow(ctx, sql, args...)}
}

// Query sends a query and answers the result set.
//
// The error pgx returns here is folded into the result rather than returned
// beside it, because the contract gives Query no error to return: a caller has
// one place to look for a failure instead of two, and Err is the place the
// contract already requires them to look.
func (s *session) Query(ctx context.Context, sql string, args ...any) driver.Rows {
	run := s.runner()
	if run == nil {
		return failedRows{err: driver.ErrSessionClosed}
	}

	sent, err := run.Query(ctx, sql, args...)
	if err != nil {
		return failedRows{err: classify(fmt.Errorf("running the query: %w", err))}
	}

	return rows{inner: sent}
}

// failedRow carries a failure that was found before the query was sent.
// QueryRow has no error to return — the contract says the error surfaces from
// Scan — so this is the shape such a failure has to travel in.
type failedRow struct{ err error }

func (r failedRow) Scan(...any) error { return r.err }

// BeginSnapshot opens the transaction a read of many statements needs: one
// snapshot for all of them, and no way to write.
//
// REPEATABLE READ is the level that gives it. PostgreSQL takes the snapshot at
// the first statement of the transaction and every later one sees the same, so
// a schema read across eleven queries is a schema of one moment. SERIALIZABLE
// would do as well and buys nothing here — there is nothing to serialise
// against, because the transaction never writes — while costing the chance of a
// serialisation failure the caller would have to retry.
func (s *session) BeginSnapshot(ctx context.Context) error {
	return s.begin(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
}

func (s *session) Begin(ctx context.Context) error {
	return s.begin(ctx, pgx.TxOptions{})
}

func (s *session) begin(ctx context.Context, options pgx.TxOptions) error {
	if s.tx != nil {
		return driver.ErrTransactionActive
	}

	if s.conn == nil {
		return driver.ErrSessionClosed
	}

	tx, err := s.conn.BeginTx(ctx, options)
	if err != nil {
		return fmt.Errorf("opening a transaction: %w", err)
	}
	s.tx = tx

	return nil
}

func (s *session) Commit(ctx context.Context) error {
	if s.tx == nil {
		return driver.ErrNoTransaction
	}

	err := s.tx.Commit(ctx)
	s.tx = nil
	if err != nil {
		return fmt.Errorf("committing: %w", err)
	}

	return nil
}

func (s *session) Rollback(ctx context.Context) error {
	if s.tx == nil {
		return driver.ErrNoTransaction
	}

	err := s.tx.Rollback(ctx)
	s.tx = nil
	if err != nil {
		return fmt.Errorf("rolling back: %w", err)
	}

	return nil
}

func (s *session) InTransaction() bool {
	return s.tx != nil
}

// Close rolls back an open transaction before releasing the connection.
//
// Releasing with a transaction still open would leave the outcome to whatever
// the server does with an abandoned backend, which is a decision nobody wrote
// down. Rolling back makes it explicit: work that was never committed is lost,
// which is what a user closing a tab means.
func (s *session) Close() {
	if s.conn == nil {
		return
	}

	if s.tx != nil {
		// The caller's context is gone by the time a tab closes, so this needs
		// one of its own — and it needs a deadline. A rollback is an ordinary
		// statement: against a backend that stopped answering without closing
		// the socket it would wait forever, and the pool that is trying to
		// release this connection would wait behind it.
		ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		_ = s.tx.Rollback(ctx)
		cancel()
		s.tx = nil
	}

	s.conn.Release()
	s.conn = nil
}
