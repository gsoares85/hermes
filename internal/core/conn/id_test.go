package conn_test

import (
	"errors"
	"regexp"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
)

// A collision would be two connections sharing one entry in the keychain: the
// second one saved would overwrite the password of the first, and the first
// would start failing to authenticate for no visible reason.
func TestNewIDNeverRepeatsItself(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, 10_000)
	for range 10_000 {
		id := conn.NewID()
		if seen[id] {
			t.Fatalf("NewID returned %q twice", id)
		}
		seen[id] = true
	}
}

func TestNewIDLooksLikeAnIdentifier(t *testing.T) {
	t.Parallel()

	shape := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

	if id := conn.NewID(); !shape.MatchString(id) {
		t.Errorf("NewID() = %q, want a version 4 UUID", id)
	}
}

// An identifier is what a saved connection has, not what a usable one needs:
// a connection opened once from a URI has no file and no entry in the keychain,
// and refusing it here would make the identifier a precondition for connecting.
func TestAConnectionWithoutAnIdentifierStillValidates(t *testing.T) {
	t.Parallel()

	config := conn.Config{Host: "localhost", Port: 5432, User: "hermes"}

	if err := config.Validate(); err != nil {
		t.Errorf("Validate() = %v, want a connection with no identifier to be usable", err)
	}
}

// The name of the field is what lets a caller reading a file point at the line
// the mistake is on, so it is part of the contract rather than a detail of the
// message.
func TestValidateNamesTheFieldThatIsWrong(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		break_ func(*conn.Config)
		field  string
	}{
		"no host":       {func(c *conn.Config) { c.Host = "" }, "host"},
		"port zero":     {func(c *conn.Config) { c.Port = 0 }, "port"},
		"port too high": {func(c *conn.Config) { c.Port = 70000 }, "port"},
		"no user":       {func(c *conn.Config) { c.User = "" }, "user"},
		"unknown mode":  {func(c *conn.Config) { c.TLS.Mode = "sometimes" }, "sslmode"},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			config := conn.Config{ID: conn.NewID(), Host: "localhost", Port: 5432, User: "hermes"}
			testCase.break_(&config)

			err := config.Validate()

			var invalid conn.InvalidField
			if !errors.As(err, &invalid) {
				t.Fatalf("Validate() = %v, want an error naming the field", err)
			}
			if invalid.Field != testCase.field {
				t.Errorf("the error blames %q, want %q", invalid.Field, testCase.field)
			}
			if !errors.Is(err, conn.ErrInvalidConfig) {
				t.Errorf("Validate() = %v, want it to still be an ErrInvalidConfig", err)
			}
		})
	}
}
