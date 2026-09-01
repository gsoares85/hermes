//go:build integration

package postgres_test

import (
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/driver"
	"github.com/gsoares85/hermes/internal/driver/postgres"
	"github.com/gsoares85/hermes/internal/testsupport"
)

// tlsConfig parses the instance DSN and applies a mode and the certificates.
func tlsConfig(t *testing.T, instance *testsupport.TLSInstance, mode conn.SSLMode) conn.Config {
	t.Helper()

	config, err := conn.ParseURI(instance.DSN)
	if err != nil {
		t.Fatalf("parsing the DSN: %v", err)
	}
	config.TLS = conn.TLS{Mode: mode, RootCert: instance.Certificates.CACert}

	return config
}

func pingWith(t *testing.T, config conn.Config) error {
	t.Helper()

	pool, err := postgres.New().Open(t.Context(), config.Target())
	if err != nil {
		return err
	}
	defer pool.Close()

	return pool.Ping(t.Context())
}

// Every mode libpq defines, against a server that really does speak TLS.
func TestEverySSLModeConnects(t *testing.T) {
	t.Parallel()

	instance := testsupport.StartPostgresTLS(t, testsupport.SupportedVersions[0])

	for _, mode := range conn.SSLModes() {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()

			if err := pingWith(t, tlsConfig(t, instance, mode)); err != nil {
				t.Errorf("sslmode=%s: %v", mode, err)
			}
		})
	}
}

// The test that gives the others their meaning. verify-full checks the hostname
// against the certificate, so reaching the same server by an address it was not
// issued for has to fail — otherwise the strict modes are only ceremony.
func TestVerifyFullRefusesTheWrongHostname(t *testing.T) {
	t.Parallel()

	instance := testsupport.StartPostgresTLS(t, testsupport.SupportedVersions[0])

	config := tlsConfig(t, instance, conn.SSLVerifyFull)
	config.Host = testsupport.WrongHost

	if err := pingWith(t, config); err == nil {
		t.Fatal("verify-full accepted a host the certificate was not issued for")
	}
}

// verify-ca checks the chain but not the name, so the same address verify-full
// refuses has to be accepted here. Together the two prove the modes differ in
// the way libpq says they do, rather than both quietly doing the same thing.
func TestVerifyCAAcceptsTheWrongHostname(t *testing.T) {
	t.Parallel()

	instance := testsupport.StartPostgresTLS(t, testsupport.SupportedVersions[0])

	config := tlsConfig(t, instance, conn.SSLVerifyCA)
	config.Host = testsupport.WrongHost

	if err := pingWith(t, config); err != nil {
		t.Errorf("verify-ca refused a valid chain over an unmatched hostname: %v", err)
	}
}

// A chain that does not lead to the configured root must be refused, or
// verify-ca would be checking nothing at all.
func TestVerifyCARefusesAnUnknownAuthority(t *testing.T) {
	t.Parallel()

	instance := testsupport.StartPostgresTLS(t, testsupport.SupportedVersions[0])
	stranger := testsupport.NewCertificates(t)

	config := tlsConfig(t, instance, conn.SSLVerifyCA)
	config.TLS.RootCert = stranger.CACert

	if err := pingWith(t, config); err == nil {
		t.Fatal("verify-ca accepted a server signed by an authority it was not given")
	}
}

// Client certificates are carried through to the server. The connection is made
// with them present and the server configured to accept them; what is proven
// here is that the paths reach libpq at all, which is what CON-07 asks for.
func TestClientCertificateIsUsed(t *testing.T) {
	t.Parallel()

	instance := testsupport.StartPostgresTLS(t, testsupport.SupportedVersions[0])

	config := tlsConfig(t, instance, conn.SSLVerifyFull)
	config.TLS.Cert = instance.Certificates.ClientCert
	config.TLS.Key = instance.Certificates.ClientKey

	if err := pingWith(t, config); err != nil {
		t.Errorf("connecting with a client certificate: %v", err)
	}
}

// A certificate path that does not exist has to fail when the connection is
// configured, not silently downgrade to a connection without it.
func TestAMissingCertificateFileIsAnError(t *testing.T) {
	t.Parallel()

	config := conn.Config{
		Host: "localhost", Port: 5432, Database: "app", User: "hermes",
		TLS: conn.TLS{Mode: conn.SSLVerifyFull, RootCert: "/no/such/ca.pem"},
	}

	var pool driver.Pool
	pool, err := postgres.New().Open(t.Context(), config.Target())
	if pool != nil {
		pool.Close()
	}
	if err == nil {
		t.Fatal("Open accepted a root certificate path that does not exist")
	}
}

// TLS works on every supported major, not only the one the rest of these tests
// use. Servers differ in their defaults and in the OpenSSL they were built
// against, and this is the cheapest place to find that out.
func TestTLSWorksOnEverySupportedVersion(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run("postgres-"+version, func(t *testing.T) {
			t.Parallel()

			instance := testsupport.StartPostgresTLS(t, version)

			if err := pingWith(t, tlsConfig(t, instance, conn.SSLVerifyFull)); err != nil {
				t.Errorf("verify-full against PostgreSQL %s: %v", version, err)
			}
		})
	}
}
