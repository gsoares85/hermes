package conn_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/driver"
)

// stubPool stands in for an engine. The seam exists so that the state machine
// can be driven through every transition without a database.
type stubPool struct {
	mu      sync.Mutex
	pingErr error
	pings   int
	closed  bool
}

func (s *stubPool) Ping(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pings++

	return s.pingErr
}

func (s *stubPool) Session(context.Context) (driver.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pingErr != nil {
		return nil, s.pingErr
	}

	// A nil session is enough: what the tests here look at is the state the
	// connection records, not what is done with the session afterwards.
	return nil, nil
}

func (s *stubPool) ServerVersion(context.Context) (string, error) { return "16.2", nil }

func (s *stubPool) Databases(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pingErr != nil {
		return nil, s.pingErr
	}

	return []string{"hermes", "postgres"}, nil
}

func (s *stubPool) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

func (s *stubPool) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pingErr = err
}

func (s *stubPool) recover() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pingErr = nil
}

func (s *stubPool) pingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.pings
}

type stubOpener struct {
	pool   *stubPool
	openEr error
}

func (s stubOpener) Open(context.Context, driver.Target) (driver.Pool, error) {
	if s.openEr != nil {
		return nil, s.openEr
	}

	return s.pool, nil
}

func openStub(t *testing.T) (*conn.Connection, *stubPool) {
	t.Helper()

	pool := &stubPool{}
	connection, err := conn.Open(t.Context(), stubOpener{pool: pool}, sample())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(connection.Close)

	return connection, pool
}

// A pool that has not been used yet is not connected, and saying otherwise
// would have the window claim a connection nobody has made.
func TestAFreshConnectionIsIdle(t *testing.T) {
	t.Parallel()

	connection, pool := openStub(t)

	if got := connection.Status().State; got != conn.StateIdle {
		t.Errorf("State = %q, want %q", got, conn.StateIdle)
	}
	if pool.pingCount() != 0 {
		t.Errorf("Open reached the server %d times, want 0", pool.pingCount())
	}
}

func TestCheckMarksTheConnectionUp(t *testing.T) {
	t.Parallel()

	connection, _ := openStub(t)

	if got := connection.Check(t.Context()); got.State != conn.StateConnected {
		t.Fatalf("State = %q, want %q", got.State, conn.StateConnected)
	}
	if got := connection.Status().State; got != conn.StateConnected {
		t.Errorf("the status did not keep the result: %q", got)
	}
}

// A drop has to be visible as a state and explained as a diagnosis, not
// reported as a driver message the reader has to decode.
func TestADropIsReportedWithADiagnosis(t *testing.T) {
	t.Parallel()

	connection, pool := openStub(t)
	connection.Check(t.Context())

	pool.fail(&driver.Failure{Class: driver.FailureRefused, Err: errors.New("connection refused")})

	status := connection.Check(t.Context())
	if status.State != conn.StateDown {
		t.Fatalf("State = %q, want %q", status.State, conn.StateDown)
	}
	if status.Diagnosis.Class != driver.FailureRefused {
		t.Errorf("Diagnosis.Class = %q, want %q", status.Diagnosis.Class, driver.FailureRefused)
	}
	if status.Diagnosis.NextStep == "" {
		t.Error("a connection went down and no next step was offered")
	}
}

// The pool reconnects on its own; what this owns is noticing that it worked.
func TestRecoveryClearsTheDiagnosis(t *testing.T) {
	t.Parallel()

	connection, pool := openStub(t)
	pool.fail(&driver.Failure{Class: driver.FailureRefused, Err: errors.New("connection refused")})
	connection.Check(t.Context())

	pool.recover()

	status := connection.Check(t.Context())
	if status.State != conn.StateConnected {
		t.Fatalf("State = %q, want %q", status.State, conn.StateConnected)
	}
	if status.Diagnosis.Failed() {
		t.Errorf("the diagnosis of the old failure survived the recovery: %+v", status.Diagnosis)
	}
}

// Reading the state is what a window does on every repaint. It must never
// reach the network, or a server that is down would freeze the interface —
// which is the rule this whole layer exists under.
func TestReadingTheStatusNeverReachesTheServer(t *testing.T) {
	t.Parallel()

	connection, pool := openStub(t)
	connection.Check(t.Context())

	before := pool.pingCount()
	for range 100 {
		_ = connection.Status()
	}

	if after := pool.pingCount(); after != before {
		t.Errorf("reading the status %d times cost %d round trips, want 0", 100, after-before)
	}
}

// A cancelled check must leave the connection as it was rather than record a
// failure of the server: the caller gave up, the server did not.
func TestACancelledCheckDoesNotBlameTheServer(t *testing.T) {
	t.Parallel()

	connection, pool := openStub(t)
	connection.Check(t.Context())

	pool.fail(context.Canceled)

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	status := connection.Check(cancelled)
	if status.State != conn.StateConnected {
		t.Errorf("State = %q, want the last known state %q to survive a cancelled check",
			status.State, conn.StateConnected)
	}
}

// Every status read carries the configuration it belongs to, and none of them
// may carry the password with it.
func TestStatusNeverCarriesThePassword(t *testing.T) {
	t.Parallel()

	connection, pool := openStub(t)
	pool.fail(&driver.Failure{
		Class: driver.FailureAuth,
		Err:   errors.New("failed: postgres://hermes:s3cr3t@db.example.com/hermes"),
	})

	status := connection.Check(t.Context())
	if got := status.String(); strings.Contains(got, "s3cr3t") {
		t.Errorf("the status leaked the password: %s", got)
	}
}

// blockingPool never answers on its own, which is what a swallowed packet looks
// like, and records whether the context it was given had a deadline.
type blockingPool struct {
	stubPool
	released chan struct{}

	mu          sync.Mutex
	hadDeadline bool
	sawDeadline bool
}

func (b *blockingPool) Ping(ctx context.Context) error {
	_, set := ctx.Deadline()

	b.mu.Lock()
	b.hadDeadline = set
	b.sawDeadline = true
	b.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.released:
		return nil
	}
}

func (b *blockingPool) deadlineSeen(t *testing.T) bool {
	t.Helper()

	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.sawDeadline {
		t.Fatal("the pool was never asked anything")
	}

	return b.hadDeadline
}

type blockingOpener struct{ pool *blockingPool }

func (b blockingOpener) Open(context.Context, driver.Target) (driver.Pool, error) {
	return b.pool, nil
}

func openBlocking(t *testing.T) (*conn.Connection, *blockingPool) {
	t.Helper()

	pool := &blockingPool{released: make(chan struct{})}
	t.Cleanup(func() { close(pool.released) })

	connection, err := conn.Open(t.Context(), blockingOpener{pool}, sample())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(connection.Close)

	return connection, pool
}

// A caller that brought no deadline must still get one. Without it a check
// against a host that swallows packets waits for the operating system to give
// up, which is minutes, and the button that started it leaves the window
// hanging — the failure this layer exists to prevent, and the one an
// integration test found by hanging for ten.
func TestACheckWithoutADeadlineGetsOne(t *testing.T) {
	t.Parallel()

	connection, pool := openBlocking(t)

	// Deliberately unbounded: it is the connection's job to bound it.
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	connection.Check(ctx)

	if !pool.deadlineSeen(t) {
		t.Error("Check passed a context with no deadline to the pool")
	}
}

// And the deadline has to actually end the wait, with a diagnosis rather than a
// hang.
func TestACheckAgainstASilentServerGivesUp(t *testing.T) {
	t.Parallel()

	connection, _ := openBlocking(t)

	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 200*time.Millisecond)
	defer cancel()

	status := connection.Check(ctx)
	if status.State != conn.StateDown {
		t.Fatalf("State = %q, want %q after the check timed out", status.State, conn.StateDown)
	}
	if !status.Diagnosis.Failed() {
		t.Errorf("a timed out check produced no diagnosis: %+v", status.Diagnosis)
	}
}

// Closing a pool waits for the connections it handed out. Doing that while
// holding the write lock blocks every Status call, which is what a window does
// on each repaint — so closing a connection to a server that has gone away
// would freeze the interface drawing it. Published state first, pool second.
func TestClosingDoesNotBlockStatus(t *testing.T) {
	t.Parallel()

	pool := &closeBlockingPool{released: make(chan struct{}), entered: make(chan struct{})}
	connection, err := conn.Open(t.Context(), closeBlockingOpener{pool}, sample())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	closed := make(chan struct{})
	go func() {
		connection.Close()
		close(closed)
	}()

	// Wait until Close is actually inside pool.Close, or the read below would
	// be racing the publication rather than the lock.
	<-pool.entered

	read := make(chan conn.State, 1)
	go func() {
		read <- connection.Status().State
	}()

	select {
	case state := <-read:
		if state != conn.StateClosed {
			t.Errorf("State = %q, want %q to be visible while the pool is still closing", state, conn.StateClosed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reading the status blocked while the pool was closing")
	}

	close(pool.released)
	<-closed
}

// closeBlockingPool stands in for a pool whose Close waits on connections that
// are not coming back.
type closeBlockingPool struct {
	stubPool
	released chan struct{}
	entered  chan struct{}
}

func (c *closeBlockingPool) Close() {
	close(c.entered)
	<-c.released
}

type closeBlockingOpener struct{ pool *closeBlockingPool }

func (c closeBlockingOpener) Open(context.Context, driver.Target) (driver.Pool, error) {
	return c.pool, nil
}

// A pool hands back an idle connection without asking the server anything, so a
// session checked out successfully proves nothing about the server being alive.
func TestASuccessfulSessionDoesNotClaimTheServerIsUp(t *testing.T) {
	t.Parallel()

	connection, pool := openStub(t)
	pool.fail(&driver.Failure{Class: driver.FailureRefused, Err: errors.New("refused")})
	connection.Check(t.Context())

	if got := connection.Status().State; got != conn.StateDown {
		t.Fatalf("State = %q, want %q", got, conn.StateDown)
	}

	// The stub hands one out without complaint, the way a pool with an idle
	// connection does.
	pool.recover()
	if _, err := connection.Session(t.Context()); err != nil {
		t.Fatalf("Session returned error: %v", err)
	}

	if got := connection.Status().State; got == conn.StateConnected {
		t.Error("checking out a session was taken as proof that the server answered")
	}
}

func TestOpenReportsAFailureToBuildThePool(t *testing.T) {
	t.Parallel()

	_, err := conn.Open(t.Context(), stubOpener{openEr: errors.New("bad certificate path")}, sample())
	if err == nil {
		t.Fatal("Open returned no error for an opener that failed")
	}
}

func TestOpenRejectsAConfigThatCannotConnect(t *testing.T) {
	t.Parallel()

	broken := sample()
	broken.Host = ""

	if _, err := conn.Open(t.Context(), stubOpener{pool: &stubPool{}}, broken); !errors.Is(err, conn.ErrInvalidConfig) {
		t.Errorf("Open with an invalid config = %v, want ErrInvalidConfig", err)
	}
}

func TestCloseReleasesThePool(t *testing.T) {
	t.Parallel()

	pool := &stubPool{}
	connection, err := conn.Open(t.Context(), stubOpener{pool: pool}, sample())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	connection.Close()
	connection.Close()

	if !pool.closed {
		t.Error("closing the connection did not close the pool")
	}
	if got := connection.Status().State; got != conn.StateClosed {
		t.Errorf("State = %q, want %q", got, conn.StateClosed)
	}
}
