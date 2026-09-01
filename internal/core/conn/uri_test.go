package conn_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
)

func TestParseURI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want conn.Config
	}{
		{
			"everything spelled out",
			"postgres://hermes:s3cr3t@db.example.com:5433/app?sslmode=require",
			conn.Config{
				Host: "db.example.com", Port: 5433, Database: "app",
				User: "hermes", Password: "s3cr3t",
				TLS: conn.TLS{Mode: conn.SSLRequire},
			},
		},
		{
			"postgresql scheme",
			"postgresql://hermes@localhost/app",
			conn.Config{Host: "localhost", Port: 5432, Database: "app", User: "hermes"},
		},
		{
			"no port falls back to 5432",
			"postgres://hermes@db.example.com/app",
			conn.Config{Host: "db.example.com", Port: 5432, Database: "app", User: "hermes"},
		},
		{
			"no host falls back to localhost",
			"postgres://hermes@/app",
			conn.Config{Host: "localhost", Port: 5432, Database: "app", User: "hermes"},
		},
		{
			"ipv6 host",
			"postgres://hermes@[2001:db8::1]:5432/app",
			conn.Config{Host: "2001:db8::1", Port: 5432, Database: "app", User: "hermes"},
		},
		{
			"percent encoded password",
			"postgres://hermes:p%40ss%3Aword@localhost/app",
			conn.Config{Host: "localhost", Port: 5432, Database: "app", User: "hermes", Password: "p@ss:word"},
		},
		{
			"user in the query instead of the userinfo",
			"postgres://localhost/app?user=hermes&password=s3cr3t",
			conn.Config{Host: "localhost", Port: 5432, Database: "app", User: "hermes", Password: "s3cr3t"},
		},
		{
			"tls files",
			"postgres://hermes@localhost/app?sslmode=verify-full&sslrootcert=/ca.pem&sslcert=/c.pem&sslkey=/k.pem",
			conn.Config{
				Host: "localhost", Port: 5432, Database: "app", User: "hermes",
				TLS: conn.TLS{Mode: conn.SSLVerifyFull, RootCert: "/ca.pem", Cert: "/c.pem", Key: "/k.pem"},
			},
		},
		{
			"surrounding whitespace, as pasted",
			"  postgres://hermes@localhost/app\n",
			conn.Config{Host: "localhost", Port: 5432, Database: "app", User: "hermes"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := conn.ParseURI(tc.in)
			if err != nil {
				t.Fatalf("ParseURI returned error: %v", err)
			}
			assertConfig(t, got, tc.want)
		})
	}
}

// Anything that is not sslmode or a certificate path is a session parameter and
// has to survive: application_name and search_path are how people tell their
// connections apart in pg_stat_activity.
func TestParseURIKeepsSessionParameters(t *testing.T) {
	t.Parallel()

	got, err := conn.ParseURI("postgres://hermes@localhost/app?application_name=hermes&search_path=public&connect_timeout=10")
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}

	want := map[string]string{"application_name": "hermes", "search_path": "public", "connect_timeout": "10"}
	if len(got.Params) != len(want) {
		t.Fatalf("Params = %v, want %v", got.Params, want)
	}
	for key, value := range want {
		if got.Params[key] != value {
			t.Errorf("Params[%q] = %q, want %q", key, got.Params[key], value)
		}
	}
}

// A URI with no database is a request to connect and look around, which is the
// whole point of being able to connect without naming one.
func TestParseURIAcceptsAURIWithoutADatabase(t *testing.T) {
	t.Parallel()

	for _, uri := range []string{"postgres://hermes@localhost", "postgres://hermes@localhost/"} {
		got, err := conn.ParseURI(uri)
		if err != nil {
			t.Fatalf("ParseURI(%q) returned error: %v", uri, err)
		}
		if got.Database != "" {
			t.Errorf("Database = %q, want it left empty", got.Database)
		}
		if err := got.Validate(); err != nil {
			t.Errorf("ParseURI(%q) produced a config that does not validate: %v", uri, err)
		}
		if got.EffectiveDatabase() != conn.MaintenanceDatabase {
			t.Errorf("EffectiveDatabase() = %q, want %q", got.EffectiveDatabase(), conn.MaintenanceDatabase)
		}
	}
}

func TestParseURIRejectsWhatItCannotUse(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"empty":             "",
		"only spaces":       "   ",
		"no scheme":         "hermes@localhost/app",
		"wrong scheme":      "mysql://hermes@localhost/app",
		"http scheme":       "http://localhost/app",
		"unknown sslmode":   "postgres://hermes@localhost/app?sslmode=sometimes",
		"port not numeric":  "postgres://hermes@localhost:pgport/app",
		"port out of range": "postgres://hermes@localhost:70000/app",
		// libpq accepts a comma-separated host list for failover. Silently
		// connecting to the first one would be a surprise, so it is refused
		// until the model can carry more than one host.
		"several hosts": "postgres://hermes@h1:5432,h2:5432/app",
	}

	for name, uri := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := conn.ParseURI(uri); !errors.Is(err, conn.ErrInvalidURI) {
				t.Errorf("ParseURI(%q) error = %v, want ErrInvalidURI", uri, err)
			}
		})
	}
}

// The URI carries the password, so the error raised when it is malformed is a
// place the secret escapes if the parser echoes its input.
func TestParseURIErrorDoesNotEchoTheSecret(t *testing.T) {
	t.Parallel()

	_, err := conn.ParseURI("postgres://hermes:s3cr3t@localhost:70000/app")
	if err == nil {
		t.Fatal("ParseURI returned no error for an impossible port")
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("the error leaked the password: %v", err)
	}
}

// Whatever the URI produced has to be usable, or the form would accept a
// connection the driver refuses later for a reason nobody can see.
func TestParseURIProducesAValidConfig(t *testing.T) {
	t.Parallel()

	for _, uri := range []string{
		"postgres://hermes:s3cr3t@db.example.com:5433/app?sslmode=verify-full",
		"postgresql://hermes@localhost/app",
		"postgres://hermes@[::1]:5432/app?application_name=hermes",
	} {
		got, err := conn.ParseURI(uri)
		if err != nil {
			t.Fatalf("ParseURI(%q) returned error: %v", uri, err)
		}
		if err := got.Validate(); err != nil {
			t.Errorf("ParseURI(%q) produced a config that does not validate: %v", uri, err)
		}
	}
}

func assertConfig(t *testing.T, got, want conn.Config) {
	t.Helper()

	if got.Host != want.Host {
		t.Errorf("Host = %q, want %q", got.Host, want.Host)
	}
	if got.Port != want.Port {
		t.Errorf("Port = %d, want %d", got.Port, want.Port)
	}
	if got.Database != want.Database {
		t.Errorf("Database = %q, want %q", got.Database, want.Database)
	}
	if got.User != want.User {
		t.Errorf("User = %q, want %q", got.User, want.User)
	}
	if got.Password != want.Password {
		t.Errorf("Password = %q, want %q", got.Password, want.Password)
	}
	if got.TLS != want.TLS {
		t.Errorf("TLS = %+v, want %+v", got.TLS, want.TLS)
	}
}
