package conn_test

import (
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
)

func TestRedactHidesThePasswordOfAURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"user and password",
			"postgres://hermes:s3cr3t@localhost:5432/hermes?sslmode=disable",
			"postgres://hermes:xxxxx@localhost:5432/hermes?sslmode=disable",
		},
		{
			"postgresql scheme",
			"postgresql://admin:hunter2@db.example.com/app",
			"postgresql://admin:xxxxx@db.example.com/app",
		},
		{
			"password in the query",
			"postgres://hermes@localhost:5432/hermes?password=s3cr3t&sslmode=disable",
			"postgres://hermes@localhost:5432/hermes?password=xxxxx&sslmode=disable",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := conn.Redact(tc.in); got != tc.want {
				t.Errorf("Redact(...) = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRedactHidesThePasswordOfAKeywordString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"plain keyword",
			"host=localhost user=hermes password=s3cr3t dbname=hermes",
			"host=localhost user=hermes password=xxxxx dbname=hermes",
		},
		{
			"quoted value with spaces",
			"host=localhost password='two words' dbname=hermes",
			"host=localhost password=xxxxx dbname=hermes",
		},
		{
			"uppercase keyword",
			"HOST=localhost PASSWORD=s3cr3t",
			"HOST=localhost PASSWORD=xxxxx",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := conn.Redact(tc.in); got != tc.want {
				t.Errorf("Redact(...) = %q, want %q", got, tc.want)
			}
		})
	}
}

// Redaction must never be the reason a connection string stops being readable:
// everything that is not the secret has to survive it.
func TestRedactKeepsEverythingElse(t *testing.T) {
	t.Parallel()

	for _, dsn := range []string{
		"postgres://hermes@localhost:5432/hermes?sslmode=disable",
		"host=localhost user=hermes dbname=hermes",
		"",
		"not a connection string at all",
	} {
		if got := conn.Redact(dsn); got != dsn {
			t.Errorf("Redact(%q) = %q, want it unchanged: there is no secret in it", dsn, got)
		}
	}
}

// The point of the helper is that the secret is gone, whatever shape it arrived
// in. This is the assertion that matters, so it is made on its own.
func TestRedactLeavesNoSecretBehind(t *testing.T) {
	t.Parallel()

	const secret = "s3cr3t"

	for _, dsn := range []string{
		"postgres://hermes:" + secret + "@localhost:5432/hermes",
		"postgres://hermes@localhost/hermes?password=" + secret,
		"host=localhost password=" + secret + " dbname=hermes",
		"password='" + secret + "'",
	} {
		if got := conn.Redact(dsn); strings.Contains(got, secret) {
			t.Errorf("Redact(%q) = %q, the secret survived", dsn, got)
		}
	}
}
