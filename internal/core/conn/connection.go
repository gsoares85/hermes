package conn

import (
	"context"
	"errors"
	"fmt"
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

	mu     sync.RWMutex
	status Status
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
		status: Status{State: StateIdle, Since: time.Now()},
	}, nil
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
