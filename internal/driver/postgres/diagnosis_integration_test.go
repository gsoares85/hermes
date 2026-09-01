//go:build integration

package postgres_test

import (
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/driver"
	"github.com/gsoares85/hermes/internal/driver/postgres"
	"github.com/gsoares85/hermes/internal/testsupport"
)

// diagnose runs a connection that is expected to fail and explains the failure
// the way the application would.
func diagnose(t *testing.T, config conn.Config) conn.Diagnosis {
	t.Helper()

	pool, err := postgres.New().Open(t.Context(), config.Target())
	if err != nil {
		return conn.Diagnose(err, config)
	}
	defer pool.Close()

	return conn.Diagnose(pool.Ping(t.Context()), config)
}

// The classes that can be provoked against a server that is really there. The
// unit tests prove the mapping from a synthetic error; this proves the errors
// a real PostgreSQL produces land in the class the mapping expects.
func TestRealFailuresAreClassified(t *testing.T) {
	t.Parallel()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])

	working, err := conn.ParseURI(instance.DSN)
	if err != nil {
		t.Fatalf("parsing the DSN: %v", err)
	}

	cases := map[string]struct {
		breaks func(*conn.Config)
		want   driver.FailureClass
	}{
		"wrong password": {
			func(c *conn.Config) { c.Password = "not the password" },
			driver.FailureAuth,
		},
		"role that does not exist": {
			func(c *conn.Config) { c.User = "nobody_here" },
			driver.FailureAuth,
		},
		"database that does not exist": {
			func(c *conn.Config) { c.Database = "no_such_database" },
			driver.FailureMissingDatabase,
		},
		"nothing listening": {
			func(c *conn.Config) { c.Port = 1 },
			driver.FailureRefused,
		},
		"name that does not resolve": {
			func(c *conn.Config) { c.Host = "no-such-host.invalid" },
			driver.FailureDNS,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			broken := working.Clone()
			tc.breaks(&broken)

			got := diagnose(t, broken)

			if !got.Failed() {
				t.Fatalf("the connection was expected to fail and did not")
			}
			if got.Class != tc.want {
				t.Errorf("Class = %q, want %q (detail: %s)", got.Class, tc.want, got.Detail)
			}
			if strings.TrimSpace(got.NextStep) == "" {
				t.Errorf("no next step was offered for %q", got.Class)
			}
		})
	}
}

// A server that does not speak TLS, asked for a mode that requires it.
func TestRequiringTLSAgainstAPlainServerIsDiagnosed(t *testing.T) {
	t.Parallel()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])

	config, err := conn.ParseURI(instance.DSN)
	if err != nil {
		t.Fatalf("parsing the DSN: %v", err)
	}
	config.TLS = conn.TLS{Mode: conn.SSLRequire}

	got := diagnose(t, config)
	if got.Class != driver.FailureTLS {
		t.Errorf("Class = %q, want %q (detail: %s)", got.Class, driver.FailureTLS, got.Detail)
	}
}

// The certificate is signed by an authority the client was not given, which is
// the failure verify-ca exists to produce.
func TestAnUntrustedCertificateIsDiagnosed(t *testing.T) {
	t.Parallel()

	instance := testsupport.StartPostgresTLS(t, testsupport.SupportedVersions[0])
	stranger := testsupport.NewCertificates(t)

	config := tlsConfig(t, instance, conn.SSLVerifyCA)
	config.TLS.RootCert = stranger.CACert

	got := diagnose(t, config)
	if got.Class != driver.FailureTLS {
		t.Errorf("Class = %q, want %q (detail: %s)", got.Class, driver.FailureTLS, got.Detail)
	}
	if !strings.Contains(got.NextStep, "sslmode") {
		t.Errorf("the next step does not mention what to check: %q", got.NextStep)
	}
}

// Whatever the server says, the password must not come back in the diagnosis.
func TestADiagnosisFromARealFailureCarriesNoPassword(t *testing.T) {
	t.Parallel()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])

	config, err := conn.ParseURI(instance.DSN)
	if err != nil {
		t.Fatalf("parsing the DSN: %v", err)
	}
	config.Password = "unmistakable-secret-value"

	got := diagnose(t, config)
	whole := got.Summary + got.Cause + got.NextStep + got.Detail

	if strings.Contains(whole, "unmistakable-secret-value") {
		t.Errorf("the diagnosis leaked the password: %s", whole)
	}
}
