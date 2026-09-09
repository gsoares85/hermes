//go:build integration

package testsupport

import (
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/driver"
	"github.com/gsoares85/hermes/internal/driver/postgres"
)

// Session checks out one connection against a running instance and closes it
// when the test ends.
//
// It lives here rather than in each package's tests because every integration
// test that talks SQL does the same four steps — parse the DSN, open a pool,
// check out a session, register the two cleanups — and a copy of them per
// package is four chances to forget the cleanup. A pool left open holds
// connections until the container dies, which shows up as another test failing
// to connect rather than as this one leaking.
func Session(tb testing.TB, instance *Instance) driver.Session {
	tb.Helper()

	config, err := conn.ParseURI(instance.DSN)
	if err != nil {
		tb.Fatalf("parsing the DSN of PostgreSQL %s: %v", instance.Version, err)
	}

	pool, err := postgres.New().Open(tb.Context(), config.Target())
	if err != nil {
		tb.Fatalf("opening a pool against PostgreSQL %s: %v", instance.Version, err)
	}
	tb.Cleanup(pool.Close)

	session, err := pool.Session(tb.Context())
	if err != nil {
		tb.Fatalf("checking out a session against PostgreSQL %s: %v", instance.Version, err)
	}
	tb.Cleanup(session.Close)

	return session
}
