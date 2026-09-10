package conn_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/driver"
)

// perDatabase hands out a pool of its own for every target, and records the
// targets it was asked for.
//
// The stub opener the rest of this package uses answers one pool whatever it is
// asked, which is enough for a state machine and not enough here: browsing is
// about there being a second connection, so a double that cannot tell two
// targets apart would pass whether or not one was opened.
type perDatabase struct {
	mu      sync.Mutex
	targets []driver.Target
	pools   map[string]*stubPool
	failing map[string]error
}

func newPerDatabase() *perDatabase {
	return &perDatabase{pools: map[string]*stubPool{}, failing: map[string]error{}}
}

func (p *perDatabase) Open(_ context.Context, target driver.Target) (driver.Pool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.targets = append(p.targets, target)

	pool := &stubPool{pingErr: p.failing[target.Database]}
	p.pools[target.Database] = pool

	return pool, nil
}

func (p *perDatabase) opened() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	names := make([]string, 0, len(p.targets))
	for _, target := range p.targets {
		names = append(names, target.Database)
	}

	return names
}

// asked answers every target the opener was handed, so that a test can check
// what a browsed database was actually opened with rather than only that it was
// opened.
func (p *perDatabase) asked() []driver.Target {
	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.targets)
}

func (p *perDatabase) poolOf(database string) *stubPool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.pools[database]
}

func (p *perDatabase) refuse(database string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.failing[database] = err
}

func openBrowsable(t *testing.T) (*conn.Connection, *perDatabase) {
	t.Helper()

	opener := newPerDatabase()

	connection, err := conn.Open(t.Context(), opener, sample())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	t.Cleanup(connection.Close)

	return connection, opener
}

// Browsing another database is another connection, because PostgreSQL will not
// cross from one to another on the same one.
//
// It is the fact the whole object tree is shaped by: expanding Databases → x is
// not another query, it is another pool. Everything else about it is the same
// connection — the same host, the same credentials, the same identity — and
// only the database differs.
func TestAnotherDatabaseIsAConnectionOfItsOwn(t *testing.T) {
	t.Parallel()

	connection, opener := openBrowsable(t)

	other, err := connection.Database(t.Context(), "reporting")
	if err != nil {
		t.Fatalf("opening reporting: %v", err)
	}

	if got := other.Config().Database; got != "reporting" {
		t.Errorf("the second connection is on %q, want reporting", got)
	}

	mine, theirs := connection.Config(), other.Config()
	mine.Database, theirs.Database = "", ""

	if !reflect.DeepEqual(mine, theirs) {
		t.Errorf("browsing changed more than the database:\n got %+v\nwant %+v", theirs, mine)
	}

	if opened := opener.opened(); len(opened) != 2 {
		t.Errorf("the opener was asked for %v, want the connection's own database and reporting", opened)
	}
}

// The same database twice is the same connection.
//
// A pool per expansion would be a pool per click, and a tree somebody browses
// for an hour would hold as many connections to a server as it had nodes
// opened.
func TestTheSameDatabaseIsOpenedOnce(t *testing.T) {
	t.Parallel()

	connection, opener := openBrowsable(t)

	first, err := connection.Database(t.Context(), "reporting")
	if err != nil {
		t.Fatalf("opening reporting: %v", err)
	}

	second, err := connection.Database(t.Context(), "reporting")
	if err != nil {
		t.Fatalf("opening reporting again: %v", err)
	}

	if first != second {
		t.Error("asking twice for the same database opened two connections")
	}

	if opened := opener.opened(); len(opened) != 2 {
		t.Errorf("the opener was asked for %v, want one pool for reporting", opened)
	}
}

// Two callers asking at once still open one.
func TestTwoCallersAskingForOneDatabaseOpenOnePool(t *testing.T) {
	t.Parallel()

	connection, opener := openBrowsable(t)

	var (
		wait  sync.WaitGroup
		mu    sync.Mutex
		found []*conn.Connection
	)

	for range 8 {
		wait.Add(1)

		go func() {
			defer wait.Done()

			other, err := connection.Database(t.Context(), "reporting")
			if err != nil {
				return
			}

			mu.Lock()
			found = append(found, other)
			mu.Unlock()
		}()
	}

	wait.Wait()

	if len(found) != 8 {
		t.Fatalf("%d of 8 callers got a connection", len(found))
	}

	for _, other := range found {
		if other != found[0] {
			t.Fatal("two callers asking at once got two connections to one database")
		}
	}

	if opened := opener.opened(); len(opened) != 2 {
		t.Errorf("the opener was asked for %v, want one pool for reporting", opened)
	}
}

// Asking for the database the connection is already on answers itself.
//
// Opening a second pool to the database already open is the same waste as
// opening two for one node, and it would give the tree two statuses for one
// thing.
func TestTheDatabaseItIsAlreadyOnIsTheConnectionItself(t *testing.T) {
	t.Parallel()

	connection, opener := openBrowsable(t)

	same, err := connection.Database(t.Context(), connection.Config().Database)
	if err != nil {
		t.Fatalf("opening its own database: %v", err)
	}

	if same != connection {
		t.Error("asking for its own database opened a second connection")
	}

	if opened := opener.opened(); len(opened) != 1 {
		t.Errorf("the opener was asked for %v, want only the connection's own database", opened)
	}
}

// Closing a connection closes every database opened under it.
//
// A pool left behind is a connection held against somebody's server by an
// application that believes it closed everything.
func TestClosingAConnectionClosesTheDatabasesUnderIt(t *testing.T) {
	t.Parallel()

	opener := newPerDatabase()

	connection, err := conn.Open(t.Context(), opener, sample())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	for _, database := range []string{"reporting", "billing"} {
		if _, err := connection.Database(t.Context(), database); err != nil {
			t.Fatalf("opening %s: %v", database, err)
		}
	}

	connection.Close()

	for _, database := range []string{"hermes", "reporting", "billing"} {
		if !opener.poolOf(database).isClosed() {
			t.Errorf("the pool of %s is still open", database)
		}
	}
}

// A database that refuses is reported when it is used, and the others stay
// browsable.
//
// Opening reaches no server — the same promise Open itself makes — so the
// refusal arrives at the first thing that actually asks the server, and it
// arrives as the reason the server gave. Without CONNECT on one database the
// node says why and the tree carries on: a server where one database is closed
// to you is ordinary, and a tree that gave up on it would be useless on exactly
// the servers people share.
func TestADatabaseThatRefusesDoesNotTakeTheOthersDown(t *testing.T) {
	t.Parallel()

	connection, opener := openBrowsable(t)

	refused := errors.New("permission denied for database restricted")
	opener.refuse("restricted", refused)

	restricted, err := connection.Database(t.Context(), "restricted")
	if err != nil {
		t.Fatalf("opening a database reached the server: %v", err)
	}

	if status := restricted.Check(t.Context()); status.State != conn.StateDown {
		t.Errorf("the refused database is in state %q, want %q", status.State, conn.StateDown)
	}

	reporting, err := connection.Database(t.Context(), "reporting")
	if err != nil {
		t.Fatalf("opening reporting: %v", err)
	}

	if status := reporting.Check(t.Context()); status.State == conn.StateDown {
		t.Errorf("a database beside the refused one is in state %q", status.State)
	}
}

// A database that refused once is the same connection after the grant.
//
// The pool is kept rather than thrown away on a failure, so somebody who is
// granted access does not have to close the whole connection to get it: the
// pool reconnects on its own and the next check says so.
func TestADatabaseThatRefusedWorksAfterTheGrant(t *testing.T) {
	t.Parallel()

	connection, opener := openBrowsable(t)

	opener.refuse("restricted", errors.New("permission denied for database restricted"))

	restricted, err := connection.Database(t.Context(), "restricted")
	if err != nil {
		t.Fatalf("opening restricted: %v", err)
	}

	if status := restricted.Check(t.Context()); status.State != conn.StateDown {
		t.Fatalf("the refused database is in state %q, want %q", status.State, conn.StateDown)
	}

	opener.poolOf("restricted").recover()

	if status := restricted.Check(t.Context()); status.State == conn.StateDown {
		t.Errorf("after the grant the database is still %q", status.State)
	}
}

// A database with no name is refused before anything is opened.
func TestADatabaseWithNoNameIsRefused(t *testing.T) {
	t.Parallel()

	connection, opener := openBrowsable(t)

	if _, err := connection.Database(t.Context(), "   "); err == nil {
		t.Fatal("a database with no name answered a connection")
	}

	if opened := opener.opened(); len(opened) != 1 {
		t.Errorf("the opener was asked for %v, want nothing beyond the connection's own", opened)
	}
}

// Browsing must not lose the read-only mark.
//
// Another database is another pool, and a pool opened without the parameter is
// a connection somebody can write through — reached in two clicks from a
// connection they marked precisely so that they could not.
func TestBrowsingAnotherDatabaseCarriesTheReadOnlyMark(t *testing.T) {
	t.Parallel()

	opener := newPerDatabase()

	marked := sample()
	marked.ReadOnly = true

	connection, err := conn.Open(t.Context(), opener, marked)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(connection.Close)

	if _, err := connection.Database(t.Context(), "reporting"); err != nil {
		t.Fatalf("opening reporting: %v", err)
	}

	for _, target := range opener.asked() {
		if target.Params["default_transaction_read_only"] != "on" {
			t.Errorf("the pool for %q was opened without the read-only mark: %v",
				target.Database, target.Params)
		}
	}
}

// Browsing a connection that has been closed opens nothing.
//
// Close drains the databases it opened and closes them. A Database() arriving
// after that would recreate the map and put a live pool into a structure
// nothing will ever walk again: a connection held against somebody's server by
// an application that believes it closed everything, with no reference left
// anywhere that could close it.
//
// The window is not theoretical. Expanding a node checks out a session through
// this call, and closing the connection is a button beside the tree.
func TestBrowsingAClosedConnectionOpensNothing(t *testing.T) {
	t.Parallel()

	connection, opener := openBrowsable(t)
	connection.Close()

	if _, err := connection.Database(t.Context(), "reporting"); !errors.Is(err, conn.ErrClosed) {
		t.Errorf("Database() = %v, want ErrClosed", err)
	}

	if opened := opener.opened(); len(opened) != 1 {
		t.Errorf("the opener was asked for %v, want only the connection's own database", opened)
	}
}

// The same promise for the database the connection is already on, which is
// answered without opening anything and used to be answered whatever the state.
func TestAClosedConnectionIsNotItsOwnDatabaseEither(t *testing.T) {
	t.Parallel()

	connection, _ := openBrowsable(t)
	connection.Close()

	if _, err := connection.Database(t.Context(), sample().Database); !errors.Is(err, conn.ErrClosed) {
		t.Errorf("Database() = %v, want ErrClosed", err)
	}
}

// Browsing must not hand the second connection the first one's maps.
//
// A plain assignment copies the struct and shares the two maps inside it, which
// is exactly the "almost" the comment on Config.Clone describes as the bug it
// exists to prevent: a session parameter added to one database's connection
// would appear on every other database browsed under the same one, and on the
// connection they all came from.
//
// Nothing writes to those maps today, and that is the point of catching it now:
// the first thing that does would find the sharing rather than cause it.
func TestBrowsingDoesNotShareTheParameterMaps(t *testing.T) {
	t.Parallel()

	connection, _ := openBrowsable(t)

	other, err := connection.Database(t.Context(), "reporting")
	if err != nil {
		t.Fatalf("opening reporting: %v", err)
	}

	other.Config().Params["application_name"] = "something else"

	if got := connection.Config().Params["application_name"]; got != "hermes" {
		t.Errorf("the parameters of the connection browsed from changed to %q", got)
	}
}
