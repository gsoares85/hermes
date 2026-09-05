package secret_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/secret"
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
		// The passphrase of the client private key is a secret of the same
		// order as the password, and a URI is a place it can arrive in.
		{
			"sslpassword in the query",
			"postgres://hermes@localhost:5432/hermes?sslmode=verify-full&sslpassword=s3cr3t",
			"postgres://hermes@localhost:5432/hermes?sslmode=verify-full&sslpassword=xxxxx",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := secret.Redact(tc.in); got != tc.want {
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
		// libpq escapes a quote or a backslash inside a value with a
		// backslash, so the escaped quote does not close the value and the
		// rest of it is still the password.
		{
			"escaped single quote inside the value",
			`host=localhost password='abc\'def' dbname=hermes`,
			"host=localhost password=xxxxx dbname=hermes",
		},
		{
			"escaped double quote inside the value",
			`host=localhost password="ab\"cd" dbname=hermes`,
			"host=localhost password=xxxxx dbname=hermes",
		},
		// An unquoted value ends at a space unless the space is escaped.
		{
			"escaped space in an unquoted value",
			`password=ab\ cd dbname=hermes`,
			"password=xxxxx dbname=hermes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := secret.Redact(tc.in); got != tc.want {
				t.Errorf("Redact(...) = %q, want %q", got, tc.want)
			}
		})
	}
}

// A driver error embeds the connection string it was given, so the text that
// reaches a log is a sentence with a DSN inside it, not a bare DSN.
func TestRedactHidesAPasswordInsideAMessage(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ message, survives string }{
		"driver prefix":   {"auth: failed to connect: postgres://hermes:s3cr3t@db.example.com:5432/hermes", "db.example.com"},
		"trailing text":   {"connecting to postgres://hermes:s3cr3t@localhost/app failed after 3 tries", "failed after 3 tries"},
		"postgresql form": {"error: postgresql://hermes:s3cr3t@localhost/app is unreachable", "is unreachable"},
		"keyword form":    {"error: could not connect using host=localhost password=s3cr3t dbname=app", "dbname=app"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := secret.Redact(tc.message)
			if strings.Contains(got, "s3cr3t") {
				t.Errorf("Redact(%q) = %q, the secret survived", tc.message, got)
			}
			// Redaction must remove the secret and nothing else: an error
			// stripped of the context that makes it findable is no better
			// than one that leaks.
			if !strings.Contains(got, tc.survives) {
				t.Errorf("Redact(%q) = %q, want it to keep %q", tc.message, got, tc.survives)
			}
		})
	}
}

// The three shapes that got through, each measured before being fixed.
func TestRedactKeepsTheMessageAroundTheSecret(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ message, survives string }{
		// url.Parse used to swallow the rest of the sentence into the path and
		// hand it back percent-encoded.
		"message beginning with a URI": {
			"postgres://hermes:s3cr3t@localhost/app failed after 3 tries",
			"failed after 3 tries",
		},
		// The bare alternative used to eat up to the next space, taking the
		// most useful part of the message with it.
		"parameter after the password": {
			"error: postgres://localhost/app?password=s3cr3t&sslmode=require now",
			"sslmode=require",
		},
		// The shape the frontend boundary produces.
		"json object": {
			`binding call args {"host":"localhost","password":"s3cr3t"}`,
			`"host":"localhost"`,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := secret.Redact(tc.message)
			if strings.Contains(got, "s3cr3t") {
				t.Errorf("Redact(%q) = %q, the secret survived", tc.message, got)
			}
			if !strings.Contains(got, tc.survives) {
				t.Errorf("Redact(%q) = %q, want it to keep %q", tc.message, got, tc.survives)
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
		if got := secret.Redact(dsn); got != dsn {
			t.Errorf("Redact(%q) = %q, want it unchanged: there is no secret in it", dsn, got)
		}
	}
}

// The point of the helper is that the secret is gone, whatever shape it arrived
// in. This is the assertion that matters, so it is made on its own.
func TestRedactLeavesNoSecretBehind(t *testing.T) {
	t.Parallel()

	const leaked = "s3cr3t"

	for _, dsn := range []string{
		"postgres://hermes:" + leaked + "@localhost:5432/hermes",
		"postgres://hermes@localhost/hermes?password=" + leaked,
		"postgres://hermes@localhost/hermes?sslpassword=" + leaked,
		"postgres://hermes:pw@localhost/hermes?sslpassword=" + leaked,
		"host=localhost password=" + leaked + " dbname=hermes",
		"password='" + leaked + "'",
		// The tail after an escaped quote is part of the secret, and a
		// pattern that stops there leaves it in the output.
		`password='abc\'` + leaked + `'`,
		`password="abc\"` + leaked + `"`,
		`password=abc\ ` + leaked + ` dbname=hermes`,
	} {
		if got := secret.Redact(dsn); strings.Contains(got, leaked) {
			t.Errorf("Redact(%q) = %q, the secret survived", dsn, got)
		}
	}
}

// Error is what an error crosses on its way out of the process, so what it
// answers has to be the redacted text and nothing else.
func TestErrorRedactsTheMessage(t *testing.T) {
	t.Parallel()

	original := fmt.Errorf("dialing %s: %w",
		"postgres://hermes:s3cr3t@db.example.com:5432/app", errors.New("connection refused"))

	got := secret.Error(original)
	if got == nil {
		t.Fatal("Error(err) = nil, want an error")
	}
	if strings.Contains(got.Error(), "s3cr3t") {
		t.Errorf("Error(%v) = %v, the secret survived", original, got)
	}
	for _, kept := range []string{"db.example.com", "connection refused"} {
		if !strings.Contains(got.Error(), kept) {
			t.Errorf("Error(%v) = %v, want it to keep %q", original, got, kept)
		}
	}
}

// Nothing is what an absent failure redacts to. Callers hand their error
// straight to this on the way out, and a non-nil answer for a nil error would
// turn every success into a failure.
func TestErrorOnNilIsNil(t *testing.T) {
	t.Parallel()

	if got := secret.Error(nil); got != nil {
		t.Errorf("Error(nil) = %v, want nil", got)
	}
}

// A redaction that can be unwrapped is a redaction anyone can undo: the wrapped
// error still answers the full text. The chain is dropped on purpose, and the
// callers match their sentinels before crossing the boundary, not after.
func TestErrorKeepsNoPathBackToTheSecret(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("postgres://hermes:s3cr3t@localhost/app is unreachable")

	redacted := secret.Error(fmt.Errorf("opening the connection: %w", sentinel))

	if errors.Unwrap(redacted) != nil {
		t.Errorf("Error(...) can be unwrapped to %v", errors.Unwrap(redacted))
	}
	if errors.Is(redacted, sentinel) {
		t.Error("Error(...) still matches the error it redacted, so the original is reachable")
	}
}

// The shape Go itself prints a value in, which is the shape the slog handler
// produces when it renders an attribute it does not otherwise recognise. The
// net used to have a hole in the one format it generates: a struct with a
// password field, logged whole, came out with the password in it.
func TestRedactHidesThePasswordInARenderedValue(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ in, survives string }{
		"a struct":            {"{Host:db.example.com Password:s3cr3t}", "db.example.com"},
		"a struct with types": {`form{Host:"db.example.com", Password:"s3cr3t"}`, "db.example.com"},
		"a map":               {"map[host:db.example.com password:s3cr3t]", "db.example.com"},
		"a slice of them":     {"[{Host:db.example.com Password:s3cr3t}]", "db.example.com"},
		"the key passphrase":  {"{Mode:verify-full SSLPassword:s3cr3t}", "verify-full"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := secret.Redact(tc.in)
			if strings.Contains(got, "s3cr3t") {
				t.Errorf("Redact(%q) = %q, the secret survived", tc.in, got)
			}
			if !strings.Contains(got, tc.survives) {
				t.Errorf("Redact(%q) = %q, want it to keep %q", tc.in, got, tc.survives)
			}
		})
	}
}

// A rendered value ends at a delimiter, and swallowing that delimiter would
// corrupt the message around the secret — the closing brace is part of the
// sentence, not part of the password.
func TestRedactKeepsTheShapeOfARenderedValue(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"{Host:h Password:s3cr3t}":     "{Host:h Password:xxxxx}",
		"map[password:s3cr3t]":         "map[password:xxxxx]",
		"{Password:s3cr3t, Port:5432}": "{Password:xxxxx, Port:5432}",
	}

	for in, want := range cases {
		if got := secret.Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

// The word in a sentence is not a secret, and a redaction that mangles prose is
// one people route around. A colon followed by a space is how English writes;
// a colon followed immediately by a value is how Go prints.
func TestRedactLeavesTheWordPasswordInProseAlone(t *testing.T) {
	t.Parallel()

	for _, prose := range []string{
		"Wrong password: check the spelling and try again.",
		"password: required",
		"The password is not stored in this file.",
	} {
		if got := secret.Redact(prose); got != prose {
			t.Errorf("Redact(%q) = %q, want it unchanged: there is no secret in it", prose, got)
		}
	}
}

// A passphrase with spaces in it is the recommended kind, and the value used to
// stop at the first one: "correct" went and "horse battery" stayed, printed
// next to the placeholder. Half a secret removed is a secret leaked.
func TestRedactHidesAPasswordWithSpacesInIt(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ in, want string }{
		"to the closing brace": {
			"{Host:h Password:correct horse battery}",
			"{Host:h Password:xxxxx}",
		},
		"to the comma": {
			"{Password:two words, Port:5432}",
			"{Password:xxxxx, Port:5432}",
		},
		"in a map": {
			"map[password:correct horse battery]",
			"map[password:xxxxx]",
		},
		"the key passphrase too": {
			"{SSLPassword:my long passphrase}",
			"{SSLPassword:xxxxx}",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := secret.Redact(tc.in); got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The password file record: host:port:database:user:password.
//
// It is the only serialised form of a password Hermes writes itself, and it was
// the one shape the redaction did not know — Redact("h:5432:db:u:s3cr3t") used
// to answer itself. Nothing logs a record today, which makes this the pattern
// that closes the next slog.Debug rather than a live leak.
func TestRedactHidesThePasswordInAPasswordFileRecord(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ in, want string }{
		"a record": {
			"h:5432:db:u:s3cr3t",
			"h:5432:db:u:xxxxx",
		},
		"the four fields before it survive": {
			"db.example.com:5432:analytics:reporting:s3cr3t",
			"db.example.com:5432:analytics:reporting:xxxxx",
		},
		"a passphrase with spaces": {
			"db.example.com:5432:analytics:reporting:correct horse battery",
			"db.example.com:5432:analytics:reporting:xxxxx",
		},
		"the wildcards libpq allows": {
			"*:*:*:postgres:s3cr3t",
			"*:*:*:postgres:xxxxx",
		},
		"a host with an escaped colon": {
			`host\:one:5432:db:user:s3cr3t`,
			`host\:one:5432:db:user:xxxxx`,
		},
		"every line of a file": {
			"a:5432:d:u:first" + "\n" + "b:5433:d:u:second",
			"a:5432:d:u:xxxxx" + "\n" + "b:5433:d:u:xxxxx",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := secret.Redact(tc.in); got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The pattern decides from the shape of a whole line, so it has to leave alone
// every line that merely has colons in it. A timestamp, a host and port named
// in a sentence, and prose are not records.
func TestRedactLeavesTextThatOnlyLooksLikeARecordAlone(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"connected to db.example.com:5432 as reporting",
		"12:34:56 warning: something happened",
		"a:b:c:d:e",
		"host:5432:db:user",
	} {
		if got := secret.Redact(text); got != text {
			t.Errorf("Redact(%q) = %q, want it unchanged: it is not a record", text, got)
		}
	}
}
