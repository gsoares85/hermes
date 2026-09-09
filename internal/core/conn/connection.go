package conn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gsoares85/hermes/internal/driver"
)

// State is what a tab shows about its connection.
type State string

// CheckTimeout bounds a connection check that the caller did not bound itself.
//
// Without it a check against a host that swallows packets waits for the
// operating system to give up, which is minutes, and the button that started it
// leaves the window hanging — the exact failure this layer exists to avoid. A
// caller that wants longer, or shorter, passes a context with its own deadline.
const CheckTimeout = 10 * time.Second

const (
	// StateIdle is a connection that has been described but never used. It is
	// the state a saved connection starts in, because opening one does not
	// reach the server.
	StateIdle State = "idle"
	// StateConnected is a connection whose last use worked.
	StateConnected State = "connected"
	// StateDown is a connection whose last use failed, with a diagnosis
	// saying why.
	StateDown State = "down"
	// StateClosed is a connection that was released.
	StateClosed State = "closed"
)

// Status is the state of a connection and, when it is down, the reason.
type Status struct {
	State State
	// Since is when the connection entered this state.
	Since time.Time
	// Diagnosis explains a failure. It is empty in every other state.
	Diagnosis Diagnosis
}

// String renders the status for a log line or a status bar.
func (s Status) String() string {
	if s.Diagnosis.Failed() {
		return fmt.Sprintf("%s: %s", s.State, s.Diagnosis)
	}

	return string(s.State)
}

// Connection is a pool plus the state a window shows about it.
//
// The pool reconnects on its own; what this owns is noticing whether the last
// attempt worked and turning a failure into something readable. Reading the
// state never reaches the network, which is what keeps a server that is down
// from freezing the interface that is drawing its status.
type Connection struct {
	config Config
	pool   driver.Pool

	// opener is kept because browsing is opening. PostgreSQL does not reach
	// across databases, so the tree's Databases level is not another query on
	// this connection: it is another connection, and this is what opens it.
	opener driver.Opener

	mu     sync.RWMutex
	status Status

	// browsing guards the databases opened under this connection. It is a lock
	// of its own rather than the one above because the two protect different
	// things and are held for different lengths: status is read on every
	// repaint, and this is taken when a node is expanded.
	browsing  sync.Mutex
	databases map[string]*Connection
}

// Open builds a connection without contacting the server.
//
// The configuration is validated here rather than on first use, so that a
// mistake in the form is reported while the person is still looking at it.
func Open(ctx context.Context, opener driver.Opener, config Config) (*Connection, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	pool, err := opener.Open(ctx, config.Target())
	if err != nil {
		return nil, fmt.Errorf("opening the connection: %w", err)
	}

	return &Connection{
		config: config,
		pool:   pool,
		opener: opener,
		status: Status{State: StateIdle, Since: time.Now()},
	}, nil
}

// Database answers a connection to another database on the same server.
//
// It exists because PostgreSQL will not cross from one database to another on
// one connection, so the object tree's Databases level is not a query — it is a
// second connection, with the same host, the same credentials and the same
// identity, differing in the database alone. The password comes from the
// configuration this connection already holds, so browsing asks nobody for it
// again and it travels no further than it already had.
//
// One per database, kept until this connection closes. A pool per expansion
// would be a pool per click, and a tree somebody browses for an hour would hold
// as many connections to a server as it had nodes opened.
//
// It reaches no server, which is the promise Open makes and this keeps. A
// database the person cannot open is therefore answered here and refused later,
// at the first thing that actually asks — which is where the reason is, and it
// saves a round trip on every expansion that was going to work. A caller that
// wants to know before it draws the node calls Check on what comes back.
//
// The lock is held across the open for the same reason: opening contacts
// nothing, so holding it costs a map write, and the alternative is a second
// implementation of single flight that would save nothing.
func (c *Connection) Database(ctx context.Context, name string) (*Connection, error) {
	wanted := strings.TrimSpace(name)
	if wanted == "" {
		return nil, fmt.Errorf("%w: a database with no name", ErrInvalidConfig)
	}

	if wanted == c.config.EffectiveDatabase() {
		return c, nil
	}

	c.browsing.Lock()
	defer c.browsing.Unlock()

	if open, found := c.databases[wanted]; found {
		return open, nil
	}

	config := c.config
	config.Database = wanted

	open, err := Open(ctx, c.opener, config)
	if err != nil {
		return nil, fmt.Errorf("opening the database %s: %w", wanted, err)
	}

	if c.databases == nil {
		c.databases = map[string]*Connection{}
	}

	c.databases[wanted] = open

	return open, nil
}

// Config returns the connection this was opened for.
func (c *Connection) Config() Config {
	return c.config
}

// Status returns the last known state.
//
// It is a read of memory and nothing else. A window repaints far more often
// than a server changes state, and a status call that dialled would turn every
// repaint into a round trip — and every unreachable server into a freeze.
func (c *Connection) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.status
}

// Check contacts the server and records what happened.
//
// This is the only thing here that reaches the network. It is deliberately
// something the caller decides to do — from a refresh button, or from a
// background job — rather than something a status read does implicitly.
func (c *Connection) Check(ctx context.Context) Status {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	return c.record(c.pool.Ping(ctx))
}

// Session checks out a session, recording a failure so that a tab that cannot
// open one updates the state without a separate probe.
//
// Only the failure. A pool hands back an idle connection without asking the
// server anything, so a session that was checked out successfully is no
// evidence that the server is alive, and recording it as such would have the
// window claim a connection nobody confirmed.
func (c *Connection) Session(ctx context.Context) (driver.Session, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	session, err := c.pool.Session(ctx)
	if err != nil {
		c.record(err)

		return nil, err
	}

	return session, nil
}

// ServerVersion reads the version and records the outcome.
func (c *Connection) ServerVersion(ctx context.Context) (string, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	version, err := c.pool.ServerVersion(ctx)
	c.record(err)

	return version, err
}

// Databases lists what this connection may open, recording the outcome so that
// a failure to list is as visible as a failure to connect.
func (c *Connection) Databases(ctx context.Context) ([]string, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()

	databases, err := c.pool.Databases(ctx)
	c.record(err)

	return databases, err
}

// Close releases the pool. It is safe to call more than once.
//
// The state is published before the pool is closed, and the lock is let go in
// between. Closing a pool waits for every connection it handed out to come
// back, which is unbounded while a session is open and takes up to fifteen
// seconds otherwise — and holding the write lock across that would block every
// Status call, freezing the window that is drawing the connection it is trying
// to close.
func (c *Connection) Close() {
	c.mu.Lock()
	if c.status.State == StateClosed {
		c.mu.Unlock()
		return
	}
	c.status = Status{State: StateClosed, Since: time.Now()}
	c.mu.Unlock()

	// The databases opened to browse this server go with it. A pool left behind
	// is a connection held against somebody's server by an application that
	// believes it closed everything, and nothing else holds a reference that
	// would ever close it.
	c.browsing.Lock()
	browsed := c.databases
	c.databases = nil
	c.browsing.Unlock()

	for _, open := range browsed {
		open.Close()
	}

	c.pool.Close()
}

// withTimeout applies the default deadline unless the caller brought one. A
// caller that has thought about how long to wait is not overridden.
func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, set := ctx.Deadline(); set {
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, CheckTimeout)
}

// record turns the outcome of an operation into the state of the connection.
func (c *Connection) record(err error) Status {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.status.State == StateClosed {
		return c.status
	}

	switch {
	// The caller gave up, which says nothing about the server. Recording a
	// failure here would have a cancelled refresh report an outage.
	case errors.Is(err, context.Canceled):
		return c.status

	case err != nil:
		c.transition(StateDown, Diagnose(err, c.config))

	default:
		c.transition(StateConnected, Diagnosis{})
	}

	return c.status
}

// transition keeps Since meaning what it says: the moment the state changed,
// not the moment it was last confirmed.
func (c *Connection) transition(state State, diagnosis Diagnosis) {
	if c.status.State == state && c.status.Diagnosis.Class == diagnosis.Class {
		c.status.Diagnosis = diagnosis
		return
	}

	c.status = Status{State: state, Since: time.Now(), Diagnosis: diagnosis}
}
