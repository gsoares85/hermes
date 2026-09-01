//go:build integration

package testsupport

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Where postgres.WithSSLCert puts the files it copies. The configuration below
// has to name the same paths, which is why they are spelled out here rather
// than chosen.
const (
	containerCACert     = "/tmp/testcontainers-go/postgres/ca_cert.pem"
	containerServerCert = "/tmp/testcontainers-go/postgres/server.cert"
	containerServerKey  = "/tmp/testcontainers-go/postgres/server.key"
)

// Minimal configuration: everything not named here keeps the server default.
// A full postgresql.conf would have to be maintained against six majors that
// do not agree on every setting.
const sslConfig = `listen_addresses = '*'
ssl = on
ssl_ca_file = '` + containerCACert + `'
ssl_cert_file = '` + containerServerCert + `'
ssl_key_file = '` + containerServerKey + `'
`

// TLSInstance is a running PostgreSQL that speaks TLS, together with the
// certificates a client needs to verify it.
type TLSInstance struct {
	*Instance
	Certificates Certificates
}

// StartPostgresTLS brings up PostgreSQL with ssl on, using a throwaway CA.
//
// The server certificate is issued for localhost, which is the name the mapped
// port is reached by, so verify-full succeeds against the instance host and
// fails against WrongHost. That pair is the point of the helper: a TLS test
// that only ever checks the happy path proves the connection is encrypted, not
// that the verification actually verifies.
//
// The certificates are copied by the module's own option, which also installs
// an entrypoint that fixes their ownership. Copying them by hand leaves them
// owned by root, and the server refuses to start on a certificate it cannot
// read.
func StartPostgresTLS(t *testing.T, version string) *TLSInstance {
	t.Helper()

	certs := NewCertificates(t)
	ctx := t.Context()
	image := "postgres:" + version + "-alpine"

	container, err := postgres.Run(ctx, image,
		postgres.WithDatabase(Database),
		postgres.WithUsername(User),
		postgres.WithPassword(Password),
		postgres.WithSSLCert(certs.CACert, certs.serverCert, certs.serverKey),
		postgres.WithConfigFile(writeSSLConfig(t)),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		t.Fatalf("starting %s with TLS: %v", image, err)
	}

	t.Cleanup(func() {
		if terminateErr := testcontainers.TerminateContainer(container); terminateErr != nil {
			t.Errorf("terminating %s: %v", image, terminateErr)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=require")
	if err != nil {
		t.Fatalf("reading the connection string of %s: %v", image, err)
	}

	return &TLSInstance{
		Instance:     &Instance{Version: version, DSN: dsn, container: container},
		Certificates: certs,
	}
}

func writeSSLConfig(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "postgresql.conf")
	if err := os.WriteFile(path, []byte(sslConfig), 0o600); err != nil {
		t.Fatalf("writing the server configuration: %v", err)
	}

	return path
}
