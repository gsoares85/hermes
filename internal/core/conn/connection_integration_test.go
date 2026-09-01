//go:build integration

package conn_test

import (
	"fmt"
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
