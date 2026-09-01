package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gsoares85/hermes/internal/driver"
)

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
		return nil, fmt.Errorf("checking out a connection: %w", err)
	}

	return &session{conn: conn}, nil
}

// runner is what a statement goes through: the open transaction if there is
// one, the connection otherwise. Both pgx types already satisfy it.
type runner interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// runner picks between the two, once, so that no method below repeats the
// branch — and so that forgetting it cannot send a statement outside the
// transaction the caller opened.
func (s *session) runner() runner {
	if s.tx != nil {
		return s.tx
	}

	return s.conn
}

func (s *session) Exec(ctx context.Context, sql string, args ...any) error {
	if _, err := s.runner().Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("running the statement: %w", err)
	}

	return nil
}

func (s *session) QueryRow(ctx context.Context, sql string, args ...any) driver.Row {
	return s.runner().QueryRow(ctx, sql, args...)
}

func (s *session) Begin(ctx context.Context) error {
	if s.tx != nil {
		return driver.ErrTransactionActive
	}

	tx, err := s.conn.Begin(ctx)
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
		// The context is gone by the time a tab closes, so this cannot use the
		// caller's. A rollback on a connection about to be released is quick,
		// and pgx cancels it with the connection if it is not.
		_ = s.tx.Rollback(context.Background())
		s.tx = nil
	}

	s.conn.Release()
	s.conn = nil
}
