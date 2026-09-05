// Package driver defines the engine abstraction. PostgreSQL is the first
// implementation; a second engine is out of scope for v1, but the seam exists
// from day one.
//
// Everything here is either an interface or a value type with no third-party
// import. That is what lets the core layer program against this package while
// the only code that knows pgx lives in internal/driver/postgres, which is the
// arrangement ADR-0009 settled.
package driver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Errors a session raises about its own transaction state. They are sentinels
// because the layer above reacts to them — a tab that thinks it is inside a
// transaction and is not has to be told, not shown a driver message.
var (
	ErrNoTransaction     = errors.New("no transaction is open on this session")
	ErrTransactionActive = errors.New("a transaction is already open on this session")

	// ErrSessionClosed is what a session answers once it has been closed. A
	// tab that kept a session past its close is a bug above this layer, and
	// the layer above has to be told which bug it is — not handed a panic, and
	// not left to guess from a driver message.
	ErrSessionClosed = errors.New("the session is closed")
)

// Target is everything the engine needs to reach a server, and nothing else.
//
// It deliberately does not carry the name of a saved connection or whether it
// is archived: those describe how a person organises their connections, and the
// engine has no business knowing them.
//
// SSLMode is a plain string rather than a typed enum on purpose. The set of
// modes is a property of the engine, and a second engine would bring its own;
// the seam carries the value and lets the implementation validate it.
type Target struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string

	SSLMode  string
	RootCert string
	Cert     string
	Key      string

	// Params are session parameters sent to the server on connect.
	Params map[string]string

	// Options are client-side connection settings, which must reach the driver
	// as connection settings rather than as parameters for the server. The
	// distinction matters: some of them, such as the ones pinning the
	// authentication method, protect the connection, and passing one to the
	// server instead both loses the setting and removes the protection.
	Options map[string]string
}

// Pool hands out connections to one server.
//
// Opening a pool must not connect: a saved connection whose host is down has to
// fail when it is used, not when the application starts. The startup budget in
// the testing strategy depends on that, and so does the window opening at all.
type Pool interface {
	// Session checks out a connection for exclusive use. Two sessions from the
	// same pool are independent: a transaction open on one is invisible to the
	// other until it commits, which is what lets two tabs share a connection
	// without sharing a transaction.
	//
	// The caller closes the session, and only then does the connection go back
	// to the pool.
	Session(ctx context.Context) (Session, error)

	// Ping acquires a connection and checks that the server answers.
	Ping(ctx context.Context) error

	// ServerVersion returns the server_version setting, which decides what the
	// catalog queries and the client tools are allowed to assume.
	ServerVersion(ctx context.Context) (string, error)

	// Databases lists the databases this connection may open, so that someone
	// who connected without naming one can pick from what they actually have
	// access to rather than guess.
	//
	// It is on the contract rather than assembled above it because the answer
	// comes from a system catalog, and which catalog that is belongs to the
	// engine.
	Databases(ctx context.Context) ([]string, error)

	// Close releases every connection the pool holds. It is safe to call more
	// than once.
	Close()
}

// Row is a single result row, scanned into destinations the caller owns. It is
// this package's own interface on purpose: a pgx row must never cross the seam,
// or the core layer would depend on the driver through a return value.
type Row interface {
	Scan(dest ...any) error
}

// Rows is a result set being read one row at a time.
//
// It is this package's own interface for the same reason Row is: a pgx result
// set must never cross the seam. It is also the first thing on this contract
// that is a resource rather than a value — reading it holds the connection the
// session checked out — so the rules below are part of the contract, not advice.
//
// The shape is the one Go has settled on for cursors, and it has a trap the
// godoc has to name: Next answers false both for "no more rows" and for
// "something broke", and only Err tells the two apart. A loop that reads Next
// and never reads Err reports an empty result for a connection that died, which
// is the difference between "this schema has no tables" and "the server is
// gone".
//
//	rows := session.Query(ctx, sql)
//	defer rows.Close()
//
//	for rows.Next() {
//	    if err := rows.Scan(&name); err != nil { return err }
//	}
//
//	return rows.Err()
type Rows interface {
	// Next advances to the next row and reports whether there is one. It
	// answers false at the end of the result and on failure alike; Err says
	// which happened.
	Next() bool

	// Scan reads the current row into destinations the caller owns. It is only
	// valid after Next has answered true.
	Scan(dest ...any) error

	// Err answers the failure that ended the read, and nil when the result was
	// read to the end. It has to be checked after the loop.
	Err() error

	// Close releases the result and the connection it was holding. The caller
	// closes, always, and a deferred Close is the only shape that survives an
	// early return. It is safe to call more than once, and safe to call after
	// the result has been read to the end.
	Close()
}

// Session is one connection checked out of a pool, with a transaction scope of
// its own.
//
// Statements run inside the transaction once Begin succeeds, and directly on
// the connection otherwise. Closing a session with a transaction still open
// rolls it back: leaving the decision to the server's disconnect handling would
// make the outcome depend on timing.
type Session interface {
	// Exec runs a statement that returns no rows.
	Exec(ctx context.Context, sql string, args ...any) error

	// QueryRow runs a query expected to return a single row. The error, if
	// any, surfaces from Scan.
	QueryRow(ctx context.Context, sql string, args ...any) Row

	// Query runs a query that returns many rows.
	//
	// There is no error to return, for the same reason QueryRow has none: a
	// failure to send the query is a failure of the result, and giving it two
	// ways out would let a caller check one and miss the other. It surfaces
	// from Err, which the contract on Rows requires reading anyway.
	//
	// The result holds this session's connection until it is closed. Reading a
	// second result before closing the first is not two queries in parallel —
	// there is one connection — so a caller that needs both at once needs two
	// sessions.
	Query(ctx context.Context, sql string, args ...any) Rows

	// Begin opens a transaction. It fails with ErrTransactionActive if one is
	// already open: nested transactions are a different feature, and silently
	// reusing the outer one would make a rollback undo more than it should.
	Begin(ctx context.Context) error

	// Commit and Rollback close the transaction, and fail with
	// ErrNoTransaction when there is none.
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error

	// InTransaction reports whether a transaction is open, which is what a tab
	// shows in its status area.
	InTransaction() bool

	// Close rolls back any open transaction and returns the connection to the
	// pool. It is safe to call more than once.
	Close()
}

// Opener builds a pool for a target. It is the entry point an engine
// implementation provides and the only thing the core layer needs to be handed.
type Opener interface {
	Open(ctx context.Context, target Target) (Pool, error)
}

// String renders the target without its password.
//
// It exists for the same reason Config has one: this is the type that actually
// carries the secret across the seam, and without a rendering of its own a
// %v in an error or a logger handed the whole struct prints every field.
func (t Target) String() string {
	mode := t.SSLMode
	if mode == "" {
		mode = "default"
	}

	return fmt.Sprintf("%s@%s:%d/%s (sslmode=%s)", t.User, t.Host, t.Port, t.Database, mode)
}

// LogValue is what a structured logger prints for a target.
func (t Target) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("host", t.Host),
		slog.Int("port", t.Port),
		slog.String("database", t.Database),
		slog.String("user", t.User),
		slog.String("sslmode", t.SSLMode),
	)
}
