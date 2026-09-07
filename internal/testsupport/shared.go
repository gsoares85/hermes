//go:build integration

package testsupport

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Shared instances, one per major version per test binary.
//
// Most tests need a server, not their own server. Starting one each costs a
// full initdb per test — the single largest thing the integration suite spends
// its time on — while the tests themselves only read, or write to tables they
// name uniquely.
//
// A shared instance cannot hang off the first test that asks for it: its
// context and its cleanup die with that test while later ones still need the
// server. It is owned by the package instead, and released by StopShared from
// TestMain.
//
// The lock is held only while the map is read or written, never while a
// container starts: a three-minute startup underneath it would serialise the
// six versions that the parallel subtests exist to start at once.
var (
	sharedMu        sync.Mutex
	sharedInstances = map[string]*sharedInstance{}
)

// sharedInstance is a server that starts exactly once, however many tests ask
// for it at the same time.
type sharedInstance struct {
	once     sync.Once
	instance *Instance
}

// SharedPostgres returns a running server of the given version, starting it on
// first use and reusing it afterwards.
//
// It is for tests that leave the server usable: reading, and writing to tables
// they alone name. A test that stops the server, restarts it, or changes a
// setting has to call StartPostgres and get one of its own, or it pulls the
// ground out from under everything sharing it.
func SharedPostgres(tb testing.TB, version string) *Instance {
	tb.Helper()

	sharedMu.Lock()
	entry, known := sharedInstances[version]
	if !known {
		entry = &sharedInstance{}
		sharedInstances[version] = entry
	}
	sharedMu.Unlock()

	entry.once.Do(func() {
		entry.instance = startShared(tb, version)
	})

	if entry.instance == nil {
		tb.Fatalf("the shared PostgreSQL %s failed to start", version)
	}

	return entry.instance
}

// startShared brings up a container owned by the package rather than by a test.
func startShared(tb testing.TB, version string) *Instance {
	tb.Helper()

	// Deliberately not tb.Context(): the server outlives the test that happened
	// to be first, and a context cancelled at the end of that test would take
	// the container with it.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	image := "postgres:" + version + "-alpine"

	container, err := postgres.Run(ctx, image,
		postgres.WithDatabase(Database),
		postgres.WithUsername(User),
		postgres.WithPassword(Password),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		tb.Fatalf("starting the shared %s: %v", image, err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		// The only chance to terminate it. Nothing holds a reference yet:
		// the instance is never returned, so StopShared has nothing to find,
		// and a shared server cannot register a t.Cleanup the way the
		// per-test helpers do — outliving the test that started it is the
		// whole point of it. Without this the container runs until the reaper
		// takes it, and the reaper is something a run can be told to skip.
		if terminateErr := testcontainers.TerminateContainer(container); terminateErr != nil {
			tb.Errorf("terminating the shared %s: %v", image, terminateErr)
		}

		tb.Fatalf("reading the connection string of the shared %s: %v", image, err)
	}

	return &Instance{Version: version, DSN: dsn, container: container}
}

// StopShared terminates every shared instance. Call it from TestMain after
// m.Run, which is the only place that runs once the tests are finished.
func StopShared() {
	sharedMu.Lock()
	defer sharedMu.Unlock()

	for version, entry := range sharedInstances {
		if entry.instance != nil {
			_ = testcontainers.TerminateContainer(entry.instance.container)
		}
		delete(sharedInstances, version)
	}
}
