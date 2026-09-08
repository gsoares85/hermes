//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/driver"
	"github.com/gsoares85/hermes/internal/driver/postgres"
	"github.com/gsoares85/hermes/internal/testsupport"
)

// openPool brings up a container and returns a pool against it.
func openPool(t *testing.T, version string) driver.Pool {
	t.Helper()

	instance := testsupport.SharedPostgres(t, version)

	config, err := conn.ParseURI(instance.DSN)
	if err != nil {
		t.Fatalf("parsing the DSN: %v", err)
	}

	pool, err := postgres.New().Open(t.Context(), config.Target())
	if err != nil {
		t.Fatalf("opening a pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}

func openSession(t *testing.T, pool driver.Pool) driver.Session {
	t.Helper()

	session, err := pool.Session(t.Context())
	if err != nil {
		t.Fatalf("checking out a session: %v", err)
	}
	t.Cleanup(session.Close)

	return session
}

// Distinguishes the tables of one test from another's, and of one run from the
// next. A counter rather than a random value so that a name in a server log
// still says which test made it.
var tableSequence atomic.Uint64

// dropTimeout bounds the cleanup drop. The test is over either way; what is not
// acceptable is a suite that hangs on tidying up.
const dropTimeout = 10 * time.Second

// createTable makes a table under a name nobody else uses and drops it when the
// test ends.
//
// The server is shared by every test in the binary and outlives all of them, so
// a fixed name is a table that is already there the second time anything
// creates it: a repeat run, or -count above one, would fail in the setup of a
// test rather than in the thing it was written to check.
func createTable(t *testing.T, session driver.Session, prefix, definition string) string {
	t.Helper()

	name := fmt.Sprintf("%s_%d", prefix, tableSequence.Add(1))
	if err := session.Exec(t.Context(), "CREATE TABLE "+name+" "+definition); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}

	t.Cleanup(func() {
		// A context of its own: the one the test carries is already cancelled
		// by the time a cleanup runs.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), dropTimeout)
		defer cancel()

		// IF EXISTS because a test that failed before committing never created
		// it, and a cleanup that fails on that would report a second problem
		// on top of the real one.
		if err := session.Exec(ctx, "DROP TABLE IF EXISTS "+name); err != nil {
			t.Errorf("dropping %s: %v", name, err)
		}
	})

	return name
}

func countRows(t *testing.T, session driver.Session, table string) int {
	t.Helper()

	var count int
	if err := session.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("counting rows in %s: %v", table, err)
	}

	return count
}

// The acceptance criterion of this task, stated as a test: two tabs on one
// connection do not share a transaction. What one writes inside an open
// transaction stays invisible to the other until it commits.
func TestTwoSessionsDoNotShareATransaction(t *testing.T) {
	t.Parallel()

	pool := openPool(t, testsupport.SupportedVersions[0])
	first := openSession(t, pool)
	second := openSession(t, pool)

	tabs := createTable(t, first, "tabs", "(id int)")

	if err := first.Begin(t.Context()); err != nil {
		t.Fatalf("opening a transaction: %v", err)
	}
	if err := first.Exec(t.Context(), "INSERT INTO "+tabs+" VALUES (1)"); err != nil {
		t.Fatalf("inserting inside the transaction: %v", err)
	}

	// The writer sees its own uncommitted row.
	if got := countRows(t, first, tabs); got != 1 {
		t.Errorf("the writing session sees %d rows, want 1", got)
	}
	// The other tab does not.
	if got := countRows(t, second, tabs); got != 0 {
		t.Errorf("the other session sees %d rows before the commit, want 0", got)
	}

	if err := first.Commit(t.Context()); err != nil {
		t.Fatalf("committing: %v", err)
	}

	if got := countRows(t, second, tabs); got != 1 {
		t.Errorf("the other session sees %d rows after the commit, want 1", got)
	}
}

func TestRollbackDiscardsTheWork(t *testing.T) {
	t.Parallel()

	pool := openPool(t, testsupport.SupportedVersions[0])
	session := openSession(t, pool)

	discarded := createTable(t, session, "discarded", "(id int)")

	if err := session.Begin(t.Context()); err != nil {
		t.Fatalf("opening a transaction: %v", err)
	}
	if err := session.Exec(t.Context(), "INSERT INTO "+discarded+" VALUES (1)"); err != nil {
		t.Fatalf("inserting: %v", err)
	}
	if err := session.Rollback(t.Context()); err != nil {
		t.Fatalf("rolling back: %v", err)
	}

	if got := countRows(t, session, discarded); got != 0 {
		t.Errorf("after the rollback the table has %d rows, want 0", got)
	}
	if session.InTransaction() {
		t.Error("the session still reports a transaction after rolling back")
	}
}

// Closing a tab with work in flight must not leave it half applied, and must
// not depend on what the server decides to do with an abandoned backend.
func TestCloseRollsBackAnOpenTransaction(t *testing.T) {
	t.Parallel()

	pool := openPool(t, testsupport.SupportedVersions[0])
	setup := openSession(t, pool)

	abandoned := createTable(t, setup, "abandoned", "(id int)")

	abandoning, err := pool.Session(t.Context())
	if err != nil {
		t.Fatalf("checking out a session: %v", err)
	}
	if err := abandoning.Begin(t.Context()); err != nil {
		t.Fatalf("opening a transaction: %v", err)
	}
	if err := abandoning.Exec(t.Context(), "INSERT INTO "+abandoned+" VALUES (1)"); err != nil {
		t.Fatalf("inserting: %v", err)
	}
	abandoning.Close()
	abandoning.Close() // twice, the way a defer and an explicit close collide

	if got := countRows(t, setup, abandoned); got != 0 {
		t.Errorf("the abandoned transaction left %d rows behind, want 0", got)
	}
}

func TestTransactionStateIsReported(t *testing.T) {
	t.Parallel()

	pool := openPool(t, testsupport.SupportedVersions[0])
	session := openSession(t, pool)

	if session.InTransaction() {
		t.Error("a fresh session reports a transaction")
	}
	if err := session.Begin(t.Context()); err != nil {
		t.Fatalf("opening a transaction: %v", err)
	}
	if !session.InTransaction() {
		t.Error("a session with an open transaction reports none")
	}
	if err := session.Commit(t.Context()); err != nil {
		t.Fatalf("committing: %v", err)
	}
	if session.InTransaction() {
		t.Error("the session still reports a transaction after committing")
	}
}

// The state machine has to say what happened rather than let a driver message
// through: a tab that thinks it is inside a transaction and is not needs to be
// told exactly that.
func TestTransactionStateErrors(t *testing.T) {
	t.Parallel()

	pool := openPool(t, testsupport.SupportedVersions[0])
	session := openSession(t, pool)

	if err := session.Commit(t.Context()); !errors.Is(err, driver.ErrNoTransaction) {
		t.Errorf("Commit with no transaction = %v, want ErrNoTransaction", err)
	}
	if err := session.Rollback(t.Context()); !errors.Is(err, driver.ErrNoTransaction) {
		t.Errorf("Rollback with no transaction = %v, want ErrNoTransaction", err)
	}

	if err := session.Begin(t.Context()); err != nil {
		t.Fatalf("opening a transaction: %v", err)
	}
	if err := session.Begin(t.Context()); !errors.Is(err, driver.ErrTransactionActive) {
		t.Errorf("Begin twice = %v, want ErrTransactionActive", err)
	}
}

// Sessions are independent on every supported major, not just the oldest.
func TestSessionsWorkOnEverySupportedVersion(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run("postgres-"+version, func(t *testing.T) {
			t.Parallel()

			pool := openPool(t, version)
			session := openSession(t, pool)

			if err := session.Begin(t.Context()); err != nil {
				t.Fatalf("opening a transaction: %v", err)
			}
			versions := createTable(t, session, "versions", "(id int)")
			if err := session.Commit(t.Context()); err != nil {
				t.Fatalf("committing: %v", err)
			}

			if got := countRows(t, session, versions); got != 0 {
				t.Errorf("the committed table has %d rows, want 0", got)
			}
		})
	}
}

// The same guard against a real closed session rather than a hand-built one:
// this is the path where the connection was genuinely acquired and released, so
// it proves the field the guard reads is the field Close actually clears.
func TestAClosedSessionRefusesWorkOnARealServer(t *testing.T) {
	t.Parallel()

	pool := openPool(t, testsupport.SupportedVersions[0])

	session, err := pool.Session(t.Context())
	if err != nil {
		t.Fatalf("checking out a session: %v", err)
	}
	session.Close()

	if err := session.Exec(t.Context(), "SELECT 1"); !errors.Is(err, driver.ErrSessionClosed) {
		t.Errorf("Exec on a closed session = %v, want ErrSessionClosed", err)
	}

	var value int
	if err := session.QueryRow(t.Context(), "SELECT 1").Scan(&value); !errors.Is(err, driver.ErrSessionClosed) {
		t.Errorf("Scan after QueryRow on a closed session = %v, want ErrSessionClosed", err)
	}

	if err := session.Begin(t.Context()); !errors.Is(err, driver.ErrSessionClosed) {
		t.Errorf("Begin on a closed session = %v, want ErrSessionClosed", err)
	}

	// Unchanged: with no transaction open, these already answered for
	// themselves and the guard must not have taken that over.
	if err := session.Commit(t.Context()); !errors.Is(err, driver.ErrNoTransaction) {
		t.Errorf("Commit on a closed session = %v, want ErrNoTransaction", err)
	}
	if session.InTransaction() {
		t.Error("a closed session reports an open transaction")
	}
}

// The seam reads a result set, which is the thing the catalog introspection is
// built on: every query it makes returns many rows, and until now the contract
// had no way to carry them.
//
// One version is enough here, unlike the catalog queries above it. What this
// exercises is the adapter between pgx and the seam, and that adapter is the
// same code on every server; the queries that differ by version are the ones
// that read pg_catalog, and those are tested where they live.
func TestQueryReadsEveryRowOfAResult(t *testing.T) {
	t.Parallel()

	pool := openPool(t, testsupport.SupportedVersions[0])
	session := openSession(t, pool)

	table := createTable(t, session, "rows", "(id int primary key, label text)")
	for id, label := range map[int]string{1: "one", 2: "two", 3: "three"} {
		if err := session.Exec(t.Context(),
			"INSERT INTO "+table+" (id, label) VALUES ($1, $2)", id, label); err != nil {
			t.Fatalf("inserting %d: %v", id, err)
		}
	}

	rows := session.Query(t.Context(), "SELECT id, label FROM "+table+" ORDER BY id")
	defer rows.Close()

	read := map[int]string{}
	for rows.Next() {
		var (
			id    int
			label string
		)
		if err := rows.Scan(&id, &label); err != nil {
			t.Fatalf("scanning a row: %v", err)
		}
		read[id] = label
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("reading the result: %v", err)
	}

	want := map[int]string{1: "one", 2: "two", 3: "three"}
	if !reflect.DeepEqual(read, want) {
		t.Errorf("the result is %v, want %v", read, want)
	}
}

// A result set holds the connection of the session it came from, so closing it
// has to give that connection back. Without this the introspection of a schema
// — which is one query per kind of object — would strand a connection per
// query and stop on the third.
func TestAClosedResultReleasesTheConnection(t *testing.T) {
	t.Parallel()

	pool := openPool(t, testsupport.SupportedVersions[0])
	session := openSession(t, pool)

	// More reads in a row than the pool has connections, which is what makes
	// this an assertion rather than a coincidence.
	for i := range 10 {
		rows := session.Query(t.Context(), "SELECT generate_series(1, 3)")
		for rows.Next() {
			var value int
			if err := rows.Scan(&value); err != nil {
				t.Fatalf("read %d: scanning: %v", i, err)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		rows.Close()

		// Twice, because the contract says so and because a deferred Close
		// after an explicit one is the shape every caller will write.
		rows.Close()
	}
}

// The failure has to arrive as a failure. A query the server refuses answers no
// rows, and a caller that reads only the loop would take that for an empty
// table — which, for a catalog read, is a schema that looks like it has nothing
// in it.
func TestAQueryTheServerRefusesFailsThroughErr(t *testing.T) {
	t.Parallel()

	pool := openPool(t, testsupport.SupportedVersions[0])
	session := openSession(t, pool)

	rows := session.Query(t.Context(), "SELECT * FROM a_table_that_does_not_exist")
	defer rows.Close()

	if rows.Next() {
		t.Error("Next answered true for a query the server refused")
	}

	err := rows.Err()
	if err == nil {
		t.Fatal("Err() = nil for a query the server refused")
	}

	// Classified, so the layer above can tell a broken query from a broken
	// connection instead of matching on the driver's own text. The SQLSTATE is
	// the part that matters here: 42P01 is undefined_table, and carrying it
	// across the seam is what lets the catalog reader say which object was
	// missing rather than "something went wrong".
	var failure *driver.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("Err() = %v, want it classified as a driver failure", err)
	}
	if failure.SQLState != "42P01" {
		t.Errorf("the failure carries SQLSTATE %q, want 42P01", failure.SQLState)
	}
}

// Statements inside a transaction go through the transaction, and a result set
// is a statement. Reading through the connection instead would show a caller
// rows their own open transaction had already deleted.
func TestQueryInsideATransactionSeesTheTransaction(t *testing.T) {
	t.Parallel()

	pool := openPool(t, testsupport.SupportedVersions[0])
	session := openSession(t, pool)

	table := createTable(t, session, "rows_tx", "(id int primary key)")
	if err := session.Exec(t.Context(), "INSERT INTO "+table+" VALUES (1)"); err != nil {
		t.Fatalf("inserting: %v", err)
	}

	if err := session.Begin(t.Context()); err != nil {
		t.Fatalf("Begin() = %v", err)
	}
	if err := session.Exec(t.Context(), "INSERT INTO "+table+" VALUES (2)"); err != nil {
		t.Fatalf("inserting inside the transaction: %v", err)
	}

	rows := session.Query(t.Context(), "SELECT id FROM "+table+" ORDER BY id")
	var read []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		read = append(read, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading inside the transaction: %v", err)
	}
	rows.Close()

	if !reflect.DeepEqual(read, []int{1, 2}) {
		t.Errorf("the transaction read %v, want [1 2]: the query did not go through it", read)
	}

	if err := session.Rollback(t.Context()); err != nil {
		t.Fatalf("Rollback() = %v", err)
	}
	if got := countRows(t, session, table); got != 1 {
		t.Errorf("the table holds %d rows after the rollback, want 1", got)
	}
}

// A snapshot is one view of the database and refuses to write, checked against
// a server rather than by counting calls on a double.
//
// Both properties are the contract of BeginSnapshot, and both were unproven:
// the only test of it counted that the reader asked for a snapshot, which says
// nothing about whether what it got is one.
func TestASnapshotIsOneViewAndCannotWrite(t *testing.T) {
	t.Parallel()

	pool := openPool(t, testsupport.SupportedVersions[0])
	reader := openSession(t, pool)
	writer := openSession(t, pool)

	table := createTable(t, writer, "snapshot", "(id integer)")

	if err := reader.BeginSnapshot(t.Context()); err != nil {
		t.Fatalf("BeginSnapshot() = %v", err)
	}

	// The snapshot is taken at the first statement, so one is needed before the
	// change to have a view that predates it.
	if before := countIn(t, reader, table); before != 0 {
		t.Fatalf("the table starts with %d rows", before)
	}

	if err := writer.Exec(t.Context(), "INSERT INTO "+table+" VALUES (1)"); err != nil {
		t.Fatalf("inserting from the other session: %v", err)
	}

	if after := countIn(t, reader, table); after != 0 {
		t.Errorf("the snapshot sees %d rows committed after it began, want the view it opened with", after)
	}

	// And it cannot write, which is what makes a hijacked function under a
	// hostile search path unable to do anything but read.
	if err := reader.Exec(t.Context(), "INSERT INTO "+table+" VALUES (2)"); err == nil {
		t.Error("a write inside the snapshot succeeded")
	}

	if err := reader.Rollback(t.Context()); err != nil {
		t.Errorf("Rollback() = %v", err)
	}
}

// Opening a second snapshot over one already open says so, rather than quietly
// reusing it — the same answer Begin gives, and what tells a reader that the
// transaction in force is somebody else's.
func TestASecondSnapshotIsRefused(t *testing.T) {
	t.Parallel()

	session := openSession(t, openPool(t, testsupport.SupportedVersions[0]))

	if err := session.BeginSnapshot(t.Context()); err != nil {
		t.Fatalf("BeginSnapshot() = %v", err)
	}

	if err := session.BeginSnapshot(t.Context()); !errors.Is(err, driver.ErrTransactionActive) {
		t.Errorf("a second BeginSnapshot() = %v, want ErrTransactionActive", err)
	}
}

// A transaction that would not close is not forgotten.
//
// Forgetting it either way was the easy shape and it made the session lie. A
// rollback that does not land leaves the backend in a transaction; a session
// that has cleared its own record reports no transaction to the status area,
// skips the rollback Close exists to perform — the check there is for a
// transaction it no longer believes in — and accepts a Begin the server will
// refuse.
func TestATransactionThatWouldNotCloseIsNotForgotten(t *testing.T) {
	t.Parallel()

	session := openSession(t, openPool(t, testsupport.SupportedVersions[0]))

	if err := session.BeginSnapshot(t.Context()); err != nil {
		t.Fatalf("BeginSnapshot() = %v", err)
	}

	done, cancel := context.WithCancel(t.Context())
	cancel()

	if err := session.Rollback(done); err == nil {
		t.Fatal("Rollback with a dead context reported success")
	}

	if !session.InTransaction() {
		t.Error("the session says there is no transaction over a backend that is in one")
	}
}

func countIn(t *testing.T, session driver.Session, table string) int {
	t.Helper()

	var rows int
	if err := session.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&rows); err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}

	return rows
}
