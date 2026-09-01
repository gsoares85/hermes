//go:build integration

package postgres_test

import (
	"errors"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/driver"
	"github.com/gsoares85/hermes/internal/driver/postgres"
	"github.com/gsoares85/hermes/internal/testsupport"
)

// openPool brings up a container and returns a pool against it.
func openPool(t *testing.T, version string) driver.Pool {
	t.Helper()

	instance := testsupport.StartPostgres(t, version)

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

	if err := first.Exec(t.Context(), "CREATE TABLE tabs (id int)"); err != nil {
		t.Fatalf("creating the table: %v", err)
	}

	if err := first.Begin(t.Context()); err != nil {
		t.Fatalf("opening a transaction: %v", err)
	}
	if err := first.Exec(t.Context(), "INSERT INTO tabs VALUES (1)"); err != nil {
		t.Fatalf("inserting inside the transaction: %v", err)
	}

	// The writer sees its own uncommitted row.
	if got := countRows(t, first, "tabs"); got != 1 {
		t.Errorf("the writing session sees %d rows, want 1", got)
	}
	// The other tab does not.
	if got := countRows(t, second, "tabs"); got != 0 {
		t.Errorf("the other session sees %d rows before the commit, want 0", got)
	}

	if err := first.Commit(t.Context()); err != nil {
		t.Fatalf("committing: %v", err)
	}

	if got := countRows(t, second, "tabs"); got != 1 {
		t.Errorf("the other session sees %d rows after the commit, want 1", got)
	}
}

func TestRollbackDiscardsTheWork(t *testing.T) {
	t.Parallel()

	pool := openPool(t, testsupport.SupportedVersions[0])
	session := openSession(t, pool)

	if err := session.Exec(t.Context(), "CREATE TABLE discarded (id int)"); err != nil {
		t.Fatalf("creating the table: %v", err)
	}

	if err := session.Begin(t.Context()); err != nil {
		t.Fatalf("opening a transaction: %v", err)
	}
	if err := session.Exec(t.Context(), "INSERT INTO discarded VALUES (1)"); err != nil {
		t.Fatalf("inserting: %v", err)
	}
	if err := session.Rollback(t.Context()); err != nil {
		t.Fatalf("rolling back: %v", err)
	}

	if got := countRows(t, session, "discarded"); got != 0 {
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

	if err := setup.Exec(t.Context(), "CREATE TABLE abandoned (id int)"); err != nil {
		t.Fatalf("creating the table: %v", err)
	}

	abandoning, err := pool.Session(t.Context())
	if err != nil {
		t.Fatalf("checking out a session: %v", err)
	}
	if err := abandoning.Begin(t.Context()); err != nil {
		t.Fatalf("opening a transaction: %v", err)
	}
	if err := abandoning.Exec(t.Context(), "INSERT INTO abandoned VALUES (1)"); err != nil {
		t.Fatalf("inserting: %v", err)
	}
	abandoning.Close()
	abandoning.Close() // twice, the way a defer and an explicit close collide

	if got := countRows(t, setup, "abandoned"); got != 0 {
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
			if err := session.Exec(t.Context(), "CREATE TABLE versions (id int)"); err != nil {
				t.Fatalf("running a statement in the transaction: %v", err)
			}
			if err := session.Commit(t.Context()); err != nil {
				t.Fatalf("committing: %v", err)
			}

			if got := countRows(t, session, "versions"); got != 0 {
				t.Errorf("the committed table has %d rows, want 0", got)
			}
		})
	}
}
