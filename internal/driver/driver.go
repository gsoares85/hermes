// Package driver defines the engine abstraction. PostgreSQL is the first
// implementation; a second engine is out of scope for v1, but the seam exists
// from day one.
//
// Everything here is either an interface or a value type with no third-party
// import. That is what lets the core layer program against this package while
// the only code that knows pgx lives in internal/driver/postgres, which is the
// arrangement ADR-0009 settled.
package driver

import "context"

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

	// Params are session parameters applied on connect.
	Params map[string]string
}

// Pool hands out connections to one server.
//
// Opening a pool must not connect: a saved connection whose host is down has to
// fail when it is used, not when the application starts. The startup budget in
// the testing strategy depends on that, and so does the window opening at all.
type Pool interface {
	// Ping acquires a connection and checks that the server answers.
	Ping(ctx context.Context) error

	// ServerVersion returns the server_version setting, which decides what the
	// catalog queries and the client tools are allowed to assume.
	ServerVersion(ctx context.Context) (string, error)

	// Close releases every connection the pool holds. It is safe to call more
	// than once.
	Close()
}

// Opener builds a pool for a target. It is the entry point an engine
// implementation provides and the only thing the core layer needs to be handed.
type Opener interface {
	Open(ctx context.Context, target Target) (Pool, error)
}
