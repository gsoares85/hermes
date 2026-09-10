//go:build integration

package conn_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/driver"
	"github.com/gsoares85/hermes/internal/driver/postgres"
	"github.com/gsoares85/hermes/internal/testsupport"
)

func openReal(t *testing.T, instance *testsupport.Instance) *conn.Connection {
	t.Helper()

	config, err := conn.ParseURI(instance.DSN)
	if err != nil {
		t.Fatalf("parsing the DSN: %v", err)
	}

	connection, err := conn.Open(t.Context(), postgres.New(), config)
	if err != nil {
		t.Fatalf("opening the connection: %v", err)
	}
	t.Cleanup(connection.Close)

	return connection
}

// The state machine against a server that really is there, including the part
// the stub cannot prove: that a live pool actually answers.
func TestStateFollowsARealServer(t *testing.T) {
	t.Parallel()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])
	connection := openReal(t, instance)

	if got := connection.Status().State; got != conn.StateIdle {
		t.Errorf("a connection that was never used is %q, want %q", got, conn.StateIdle)
	}

	if got := connection.Check(t.Context()); got.State != conn.StateConnected {
		t.Fatalf("State = %q, want %q (%s)", got.State, conn.StateConnected, got.Diagnosis)
	}
}

// The server terminates every backend. Automatic reconnection does not mean the
// failure is invisible: the attempt that was holding the dead connection fails,
// and the next one opens a new one. Asserting otherwise was wrong about what a
// pool does.
func TestThePoolReconnectsAfterTheServerDropsIt(t *testing.T) {
	t.Parallel()

	instance := testsupport.StartPostgres(t, testsupport.SupportedVersions[0])
	connection := openReal(t, instance)

	if got := connection.Check(t.Context()); got.State != conn.StateConnected {
		t.Fatalf("the connection did not come up: %s", got)
	}

	// The closest thing to an administrator dropping the connections.
	instance.Exec(t, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname = current_database() AND pid <> pg_backend_pid()`)

	// The first attempt may or may not notice, depending on whether the pool
	// hands back the connection that was killed. Either outcome is correct.
	if first := connection.Check(t.Context()); first.State == conn.StateDown &&
		first.Diagnosis.Class != driver.FailureDropped {
		t.Errorf("Diagnosis.Class = %q, want %q for a terminated backend (%s)",
			first.Diagnosis.Class, driver.FailureDropped, first.Diagnosis.Detail)
	}

	// What must be true is that the next one works.
	if second := connection.Check(t.Context()); second.State != conn.StateConnected {
		t.Errorf("State = %q on the attempt after a drop, want the pool to have reconnected (%s)",
			second.State, second.Diagnosis)
	}
}

// A server that goes away has to be reported as down, with a diagnosis rather
// than a driver message, and without the call that noticed it hanging.
func TestAStoppedServerIsReportedAsDown(t *testing.T) {
	t.Parallel()

	instance := testsupport.StartPostgres(t, testsupport.SupportedVersions[0])
	connection := openReal(t, instance)

	if got := connection.Check(t.Context()); got.State != conn.StateConnected {
		t.Fatalf("the connection did not come up: %s", got)
	}

	instance.Stop(t)

	got := connection.Check(t.Context())
	if got.State != conn.StateDown {
		t.Fatalf("State = %q after the server was stopped, want %q", got.State, conn.StateDown)
	}
	if !got.Diagnosis.Failed() {
		t.Fatal("the connection went down without a diagnosis")
	}
	// A pooled connection sees the socket die before it sees a refusal, so a
	// drop is the usual answer here; a refusal or a timeout are the other
	// honest ones depending on what the pool was holding.
	switch got.Diagnosis.Class {
	case driver.FailureDropped, driver.FailureRefused, driver.FailureTimeout:
	default:
		t.Errorf("Diagnosis.Class = %q, want the server to be recognisably gone (%s)",
			got.Diagnosis.Class, got.Diagnosis.Detail)
	}
	if got.Diagnosis.NextStep == "" {
		t.Error("the connection went down and offered no next step")
	}

	if again := connection.Status(); again.State != conn.StateDown {
		t.Errorf("reading the status again gave %q, want %q", again.State, conn.StateDown)
	}
}

// Checking a session out of a pool does not prove the server is alive: the pool
// hands back an idle connection without asking. Only using it finds out — which
// is what this asserts, after an earlier version of it hung for twelve minutes
// by failing without releasing the session it had just been given.
func TestASessionFailureUpdatesTheState(t *testing.T) {
	t.Parallel()

	instance := testsupport.StartPostgres(t, testsupport.SupportedVersions[0])
	connection := openReal(t, instance)

	session, err := connection.Session(t.Context())
	if err != nil {
		t.Fatalf("checking out a session: %v", err)
	}
	session.Close()

	// Deliberately still idle. The pool hands back a connection without asking
	// the server anything, so checking one out is no evidence that the server
	// answered — and an earlier version of this recorded it as if it were.
	if got := connection.Status().State; got != conn.StateIdle {
		t.Errorf("State = %q after a session was checked out, want %q: a checkout proves nothing",
			got, conn.StateIdle)
	}

	// A check does reach the server, and that is what may claim it is up.
	if got := connection.Check(t.Context()); got.State != conn.StateConnected {
		t.Fatalf("State = %q after a check, want %q (%s)", got.State, conn.StateConnected, got.Diagnosis)
	}

	instance.Stop(t)

	// Released whatever happens, because a pool cannot be closed while a
	// session it handed out is still outstanding.
	after, err := connection.Session(t.Context())
	if after != nil {
		defer after.Close()
	}

	if err == nil {
		// The pool gave back an idle connection without checking it. Using it
		// is what discovers the server is gone.
		if execErr := after.Exec(t.Context(), "SELECT 1"); execErr == nil {
			t.Fatal("a statement ran against a stopped server")
		}
		connection.Check(t.Context())
	}

	if got := connection.Status(); got.State != conn.StateDown {
		t.Errorf("State = %q after the server was stopped, want %q (%s)", got.State, conn.StateDown, got.Diagnosis)
	}
}

// The point of making the database optional: connect with a host, a user and a
// password, then find out what is there.
func TestConnectingWithoutADatabaseListsWhatIsThere(t *testing.T) {
	t.Parallel()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])

	config, err := conn.ParseURI(instance.DSN)
	if err != nil {
		t.Fatalf("parsing the DSN: %v", err)
	}
	// Exactly what someone typing into an empty form would send.
	config.Database = ""

	connection, err := conn.Open(t.Context(), postgres.New(), config)
	if err != nil {
		t.Fatalf("opening a connection with no database: %v", err)
	}
	t.Cleanup(connection.Close)

	if got := connection.Check(t.Context()); got.State != conn.StateConnected {
		t.Fatalf("State = %q, want %q (%s)", got.State, conn.StateConnected, got.Diagnosis)
	}

	databases, err := connection.Databases(t.Context())
	if err != nil {
		t.Fatalf("listing the databases: %v", err)
	}

	for _, want := range []string{conn.MaintenanceDatabase, testsupport.Database} {
		if !contains(databases, want) {
			t.Errorf("the list %v does not include %q", databases, want)
		}
	}
	// Templates exist to be copied, not opened, so offering them would be
	// offering a choice that fails.
	for _, unwanted := range []string{"template0", "template1"} {
		if contains(databases, unwanted) {
			t.Errorf("the list %v includes the template %q", databases, unwanted)
		}
	}
}

// A URI with no database at all is the pasted form of the same thing.
func TestAURIWithoutADatabaseConnects(t *testing.T) {
	t.Parallel()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])

	full, err := conn.ParseURI(instance.DSN)
	if err != nil {
		t.Fatalf("parsing the DSN: %v", err)
	}

	bare := fmt.Sprintf("postgres://%s:%s@%s:%d?sslmode=disable",
		full.User, testsupport.Password, full.Host, full.Port)

	config, err := conn.ParseURI(bare)
	if err != nil {
		t.Fatalf("parsing a URI without a database: %v", err)
	}
	if config.Database != "" {
		t.Fatalf("Database = %q, want it empty", config.Database)
	}

	connection, err := conn.Open(t.Context(), postgres.New(), config)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	t.Cleanup(connection.Close)

	if got := connection.Check(t.Context()); got.State != conn.StateConnected {
		t.Errorf("State = %q, want %q (%s)", got.State, conn.StateConnected, got.Diagnosis)
	}
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}

	return false
}

// Browsing a second database really opens a second connection to it, and the
// two answer their own catalogs.
//
// The stub cannot show this: it proves a pool was asked for, not that a server
// let anybody in. What makes it worth a server is the fact the whole design
// rests on — one connection cannot see another database, so the second pool
// either exists or the tree cannot show a second database at all.
func TestASecondDatabaseIsReallyASecondConnection(t *testing.T) {
	t.Parallel()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])
	connection := openReal(t, instance)

	const browsed = "browsed_by_the_tree"

	instance.Exec(t, "DROP DATABASE IF EXISTS "+browsed)
	instance.Exec(t, "CREATE DATABASE "+browsed)

	other, err := connection.Database(t.Context(), browsed)
	if err != nil {
		t.Fatalf("opening %s: %v", browsed, err)
	}

	if status := other.Check(t.Context()); status.State != conn.StateConnected {
		t.Fatalf("the second database is %q: %s", status.State, status.Diagnosis.Summary)
	}

	if got := other.Config().Database; got != browsed {
		t.Errorf("the second connection is on %q, want %s", got, browsed)
	}

	// The first is untouched by the second, which is what "another connection"
	// has to mean for the tree to hold two databases at once.
	if status := connection.Check(t.Context()); status.State != conn.StateConnected {
		t.Errorf("the first connection is %q after browsing a second database", status.State)
	}
}

// A database that cannot be opened says why, and the ones beside it stay open.
//
// A server where one database is closed to you is ordinary, and it is exactly
// the shape a tree must survive: the node reports the reason the server gave
// and every sibling carries on.
//
// The fixture closes the database with ALLOW_CONNECTIONS rather than by
// revoking CONNECT, because the role these tests run as owns the server and a
// superuser is not subject to CONNECT — a REVOKE here leaves the database wide
// open and the test green for the wrong reason, which is what the first version
// of this did. What is being exercised is the same either way: a pool that
// cannot open, and a diagnosis with something to show.
func TestADatabaseThatCannotBeOpenedSaysWhy(t *testing.T) {
	t.Parallel()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])
	connection := openReal(t, instance)

	const locked = "locked_away_from_the_tree"

	instance.Exec(t, "DROP DATABASE IF EXISTS "+locked)
	instance.Exec(t, "CREATE DATABASE "+locked)
	instance.Exec(t, fmt.Sprintf("ALTER DATABASE %s WITH ALLOW_CONNECTIONS false", locked))

	refused, err := connection.Database(t.Context(), locked)
	if err != nil {
		t.Fatalf("opening a database reached the server: %v", err)
	}

	status := refused.Check(t.Context())
	if status.State != conn.StateDown {
		t.Fatalf("the database that refuses is %q, want %q", status.State, conn.StateDown)
	}

	if status.Diagnosis.Summary == "" {
		t.Error("the refusal carries no summary, so the node has nothing to show")
	}

	if status := connection.Check(t.Context()); status.State != conn.StateConnected {
		t.Errorf("the connection beside the refused database is %q", status.State)
	}
}

// The read-only mark, proved where it has to be true: against a server.
//
// A mark that only greys out a button is the failure this test exists to catch,
// so nothing here asks the window. The connection is opened marked, an INSERT
// is sent, and what has to come back is the server's own refusal turned into
// the sentence that says where the mark is cleared — not the driver's text.
func TestAReadOnlyConnectionIsRefusedByTheServer(t *testing.T) {
	t.Parallel()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])
	instance.Exec(t, "CREATE TABLE IF NOT EXISTS refused_write (id int)")

	config, err := conn.ParseURI(instance.DSN)
	if err != nil {
		t.Fatalf("parsing the DSN: %v", err)
	}
	config.ReadOnly = true

	connection, err := conn.Open(t.Context(), postgres.New(), config)
	if err != nil {
		t.Fatalf("opening the connection: %v", err)
	}
	t.Cleanup(connection.Close)

	session, err := connection.Session(t.Context())
	if err != nil {
		t.Fatalf("checking out a session: %v", err)
	}
	t.Cleanup(session.Close)

	err = session.Exec(t.Context(), "INSERT INTO refused_write VALUES (1)")
	if err == nil {
		t.Fatal("the server accepted a write on a connection marked read-only")
	}

	if class, _ := driver.ClassOf(err); class != driver.FailureReadOnly {
		t.Fatalf("class = %q, want %q (%v)", class, driver.FailureReadOnly, err)
	}

	diagnosis := conn.Diagnose(err, config)
	explanation := strings.ToLower(diagnosis.Summary + diagnosis.Cause + diagnosis.NextStep)

	if !strings.Contains(explanation, "read-only") {
		t.Errorf("the refusal was not explained: %+v", diagnosis)
	}
	if strings.Contains(diagnosis.Summary, "SQLSTATE") {
		t.Errorf("the summary is the driver's own text: %q", diagnosis.Summary)
	}
}

// The other half of the same promise. A gate that refused every write would
// pass the test above and leave the product unable to do anything, so the
// unmarked connection has to still write.
func TestAnUnmarkedConnectionStillWrites(t *testing.T) {
	t.Parallel()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])
	instance.Exec(t, "CREATE TABLE IF NOT EXISTS accepted_write (id int)")

	connection := openReal(t, instance)

	session, err := connection.Session(t.Context())
	if err != nil {
		t.Fatalf("checking out a session: %v", err)
	}
	t.Cleanup(session.Close)

	if err := session.Exec(t.Context(), "INSERT INTO accepted_write VALUES (1)"); err != nil {
		t.Errorf("an unmarked connection was refused a write: %v", err)
	}
}

// The read-only mark reaches every database browsed under the connection.
//
// Another database is another pool, opened from the same configuration, and a
// pool opened without the parameter would be a connection somebody can write
// through — reached in two clicks from a connection they marked precisely so
// that they could not. The unit test proves the parameter is on the target;
// this proves the server acts on it, which is the only claim that matters.
func TestBrowsingCarriesTheReadOnlyMarkToTheServer(t *testing.T) {
	t.Parallel()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])

	config, err := conn.ParseURI(instance.DSN)
	if err != nil {
		t.Fatalf("parsing the DSN: %v", err)
	}
	config.ReadOnly = true

	connection, err := conn.Open(t.Context(), postgres.New(), config)
	if err != nil {
		t.Fatalf("opening the connection: %v", err)
	}
	t.Cleanup(connection.Close)

	// The maintenance database, which every server has and which is not the one
	// the connection was opened on — so what is exercised is the second pool.
	other, err := connection.Database(t.Context(), conn.MaintenanceDatabase)
	if err != nil {
		t.Fatalf("browsing to %s: %v", conn.MaintenanceDatabase, err)
	}

	session, err := other.Session(t.Context())
	if err != nil {
		t.Fatalf("checking out a session on %s: %v", conn.MaintenanceDatabase, err)
	}
	t.Cleanup(session.Close)

	// DDL rather than an INSERT, because it needs nothing to exist first and is
	// refused by the same gate.
	err = session.Exec(t.Context(), "CREATE TABLE browsed_write (id int)")
	if err == nil {
		t.Fatal("a database browsed from a read-only connection accepted a write")
	}

	if class, _ := driver.ClassOf(err); class != driver.FailureReadOnly {
		t.Fatalf("class = %q, want %q (%v)", class, driver.FailureReadOnly, err)
	}
}
