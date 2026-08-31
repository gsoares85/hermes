//go:build integration

package testsupport

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// SupportedVersions are the PostgreSQL major versions every feature that reads
// the catalog or generates DDL has to pass against.
var SupportedVersions = []string{"13", "15", "16", "17", "18"}

// execTimeout bounds a single statement. Without it a psql that never returns
// runs until the timeout of the whole test binary.
const execTimeout = 2 * time.Minute

// Credentials of the throwaway database. They are fixed on purpose: nothing
// here is a secret, and a constant keeps failure output readable.
const (
	Database = "hermes"
	User     = "hermes"
	Password = "hermes"
)

// Instance is a running PostgreSQL container.
type Instance struct {
	Version   string
	DSN       string
	container *postgres.PostgresContainer
}

// StartPostgres brings up PostgreSQL of the given major version and returns it
// ready to accept connections. The container is terminated when the test ends.
func StartPostgres(t *testing.T, version string) *Instance {
	t.Helper()

	// Tied to the test, not to the process: a container that never comes up
	// has to die with the test that asked for it, not hold the whole run
	// until the timeout of the test binary.
	ctx := t.Context()
	image := "postgres:" + version + "-alpine"

	container, err := postgres.Run(ctx, image,
		postgres.WithDatabase(Database),
		postgres.WithUsername(User),
		postgres.WithPassword(Password),
		testcontainers.WithWaitStrategy(
			// The entrypoint starts the server once to run the init scripts and
			// again for real, so the message has to be seen twice.
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		t.Fatalf("starting %s: %v", image, err)
	}

	t.Cleanup(func() {
		if terminateErr := testcontainers.TerminateContainer(container); terminateErr != nil {
			t.Errorf("terminating %s: %v", image, terminateErr)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("reading the connection string of %s: %v", image, err)
	}

	return &Instance{Version: version, DSN: dsn, container: container}
}

// Exec runs psql inside the container and returns its combined output. It keeps
// the harness free of a SQL driver: the connection layer arrives in TASK-0002,
// and until then the container itself is the client.
func (i *Instance) Exec(t *testing.T, statement string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), execTimeout)
	defer cancel()

	// Multiplexed demuxes the Docker stream: without it the output still
	// carries the per-frame header Docker puts in front of every chunk.
	code, reader, err := i.container.Exec(ctx, []string{
		"psql", "-U", User, "-d", Database, "-t", "-A", "-c", statement,
	}, tcexec.Multiplexed())
	if err != nil {
		t.Fatalf("running %q on PostgreSQL %s: %v", statement, i.Version, err)
	}

	output := readAll(t, reader)
	if code != 0 {
		t.Fatalf("running %q on PostgreSQL %s exited with %d: %s", statement, i.Version, code, output)
	}

	return output
}
