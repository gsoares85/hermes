//go:build integration

package postgres_test

import (
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/driver/postgres"
	"github.com/gsoares85/hermes/internal/testsupport"
)

// The whole chain against a real server, on every supported major: the DSN the
// container reports is parsed the way a pasted one would be, reduced to an
// engine target, and handed to pgx.
//
// Parsing the container's own DSN rather than building a target by hand is
// deliberate. It is the only place where the URI parser meets a connection
// string written by PostgreSQL tooling instead of by a test.
func TestPoolConnectsToEverySupportedVersion(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run("postgres-"+version, func(t *testing.T) {
			t.Parallel()

			instance := testsupport.SharedPostgres(t, version)

			config, err := conn.ParseURI(instance.DSN)
			if err != nil {
				t.Fatalf("parsing the DSN of PostgreSQL %s: %v", version, err)
			}
			if err = config.Validate(); err != nil {
				t.Fatalf("the DSN of PostgreSQL %s produced an invalid config: %v", version, err)
			}

			pool, err := postgres.New().Open(t.Context(), config.Target())
			if err != nil {
				t.Fatalf("opening a pool for PostgreSQL %s: %v", version, err)
			}
			defer pool.Close()

			if err = pool.Ping(t.Context()); err != nil {
				t.Fatalf("pinging PostgreSQL %s: %v", version, err)
			}

			reported, err := pool.ServerVersion(t.Context())
			if err != nil {
				t.Fatalf("reading the version of PostgreSQL %s: %v", version, err)
			}
			// Proves the pool reached the server the test asked for, not
			// whichever container happened to answer.
			if !strings.HasPrefix(reported, version+".") && reported != version {
				t.Errorf("server_version = %q, want major %s", reported, version)
			}
		})
	}
}

// Credentials that do not work must fail on use, not on open. This is the same
// laziness the unit test proves without Docker, checked here against a server
// that really is there and really does refuse.
func TestPingFailsWithWrongCredentials(t *testing.T) {
	t.Parallel()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])

	config, err := conn.ParseURI(instance.DSN)
	if err != nil {
		t.Fatalf("parsing the DSN: %v", err)
	}
	config.Password = "not the password"

	pool, err := postgres.New().Open(t.Context(), config.Target())
	if err != nil {
		t.Fatalf("Open failed before anything was attempted: %v", err)
	}
	defer pool.Close()

	err = pool.Ping(t.Context())
	if err == nil {
		t.Fatal("Ping with a wrong password returned no error")
	}
	if strings.Contains(err.Error(), "not the password") {
		t.Errorf("the failure leaked the password: %v", err)
	}
}

// A database that does not exist is one of the seven failure classes this task
// has to diagnose. Here it only has to fail; turning it into a readable message
// is the diagnosis step.
func TestPingFailsForAMissingDatabase(t *testing.T) {
	t.Parallel()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])

	config, err := conn.ParseURI(instance.DSN)
	if err != nil {
		t.Fatalf("parsing the DSN: %v", err)
	}
	config.Database = "no_such_database"

	pool, err := postgres.New().Open(t.Context(), config.Target())
	if err != nil {
		t.Fatalf("Open failed before anything was attempted: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(t.Context()); err == nil {
		t.Fatal("Ping to a missing database returned no error")
	}
}
