package postgres

import (
	"errors"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/driver"
)

func base() driver.Target {
	return driver.Target{Host: "localhost", Port: 5432, Database: "app", User: "hermes"}
}

func TestConnStringCarriesTheAddressAndIdentity(t *testing.T) {
	t.Parallel()

	got, err := connString(base())
	if err != nil {
		t.Fatalf("connString returned error: %v", err)
	}

	for _, want := range []string{"host='localhost'", "port='5432'", "dbname='app'", "user='hermes'"} {
		if !strings.Contains(got, want) {
			t.Errorf("connString() = %q, want it to carry %s", got, want)
		}
	}
}

// The password is the one thing that must never be in this string. A connection
// string is quoted into logs and error messages by every layer that handles it,
// which is why it is set on the parsed configuration instead.
func TestConnStringNeverCarriesThePassword(t *testing.T) {
	t.Parallel()

	target := base()
	target.Password = "s3cr3t"

	got, err := connString(target)
	if err != nil {
		t.Fatalf("connString returned error: %v", err)
	}
	if strings.Contains(got, "s3cr3t") || strings.Contains(got, "password") {
		t.Errorf("connString() = %q, want no password in it", got)
	}
}

func TestConnStringCarriesEveryTLSSetting(t *testing.T) {
	t.Parallel()

	target := base()
	target.SSLMode = "verify-full"
	target.RootCert = "/etc/ssl/ca.pem"
	target.Cert = "/etc/ssl/client.pem"
	target.Key = "/etc/ssl/client.key"

	got, err := connString(target)
	if err != nil {
		t.Fatalf("connString returned error: %v", err)
	}

	for _, want := range []string{
		"sslmode='verify-full'",
		"sslrootcert='/etc/ssl/ca.pem'",
		"sslcert='/etc/ssl/client.pem'",
		"sslkey='/etc/ssl/client.key'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("connString() = %q, want it to carry %s", got, want)
		}
	}
}

// A path with a space or a quote in it is ordinary on Windows and on macOS, and
// an unescaped one would silently truncate the value or change which key the
// rest of the string sets.
func TestConnStringEscapesAwkwardValues(t *testing.T) {
	t.Parallel()

	target := base()
	target.Database = "my app"
	target.RootCert = `C:\Users\Some One\ca.pem`
	target.User = "o'brien"

	got, err := connString(target)
	if err != nil {
		t.Fatalf("connString returned error: %v", err)
	}

	if !strings.Contains(got, "dbname='my app'") {
		t.Errorf("connString() = %q, want the space kept inside the quotes", got)
	}
	if !strings.Contains(got, `sslrootcert='C:\\Users\\Some One\\ca.pem'`) {
		t.Errorf("connString() = %q, want the backslashes escaped", got)
	}
	if !strings.Contains(got, `user='o\'brien'`) {
		t.Errorf("connString() = %q, want the quote escaped", got)
	}
}

// Whatever the escaping produces has to survive the parser it was written for.
// Checking the string by eye proves the shape; parsing it proves the meaning.
func TestConnStringSurvivesTheParser(t *testing.T) {
	t.Parallel()

	target := base()
	target.Database = "my app"
	target.User = "o'brien"
	target.SSLMode = "disable"

	config, err := poolConfig(target)
	if err != nil {
		t.Fatalf("poolConfig returned error: %v", err)
	}
	if config.ConnConfig.Database != "my app" {
		t.Errorf("Database = %q, want %q", config.ConnConfig.Database, "my app")
	}
	if config.ConnConfig.User != "o'brien" {
		t.Errorf("User = %q, want %q", config.ConnConfig.User, "o'brien")
	}
}

func TestConnStringOmitsWhatWasNotSet(t *testing.T) {
	t.Parallel()

	got, err := connString(base())
	if err != nil {
		t.Fatalf("connString returned error: %v", err)
	}

	for _, absent := range []string{"sslmode", "sslrootcert", "sslcert", "sslkey"} {
		if strings.Contains(got, absent) {
			t.Errorf("connString() = %q, want no %s when it was not set", got, absent)
		}
	}
}

func TestConnStringRejectsAnUnknownSSLMode(t *testing.T) {
	t.Parallel()

	target := base()
	target.SSLMode = "sometimes"

	if _, err := connString(target); !errors.Is(err, ErrUnsupported) {
		t.Errorf("connString with an unknown sslmode = %v, want ErrUnsupported", err)
	}
}

// Every libpq mode is accepted now, including the ones the earlier step refused
// while certificate handling was not wired.
func TestConnStringAcceptsEveryLibpqMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"", "disable", "allow", "prefer", "require", "verify-ca", "verify-full"} {
		target := base()
		target.SSLMode = mode

		if _, err := connString(target); err != nil {
			t.Errorf("connString with sslmode=%q returned error: %v", mode, err)
		}
	}
}
