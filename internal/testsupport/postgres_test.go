//go:build integration

package testsupport_test

import (
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/testsupport"
)

// The whole test matrix rests on this harness, so it is proven first: every
// supported major version starts, answers a query and reports the version it
// was asked for.
func TestPostgresStartsForEverySupportedVersion(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run("postgres-"+version, func(t *testing.T) {
			t.Parallel()

			instance := testsupport.StartPostgres(t, version)

			// Reported redacted even though this password is a throwaway:
			// the first test in the repository is where the habit of never
			// printing a connection string in full either starts or does not.
			if !strings.Contains(instance.DSN, "sslmode=disable") {
				t.Errorf("DSN = %q, want it to carry the requested options", conn.Redact(instance.DSN))
			}

			if got := instance.Exec(t, "SELECT 1"); got != "1" {
				t.Errorf("SELECT 1 = %q, want %q", got, "1")
			}

			reported := instance.Exec(t, "SHOW server_version")
			if !strings.HasPrefix(reported, version+".") && reported != version {
				t.Errorf("server_version = %q, want major version %s", reported, version)
			}
		})
	}
}
