package conn

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// ErrInvalidConfig is returned by Validate for a connection that cannot be used.
var ErrInvalidConfig = errors.New("invalid connection")

// DefaultPort is the port PostgreSQL listens on unless told otherwise.
const DefaultPort = 5432

// SSLMode is one of the six values libpq accepts for sslmode.
type SSLMode string

// The libpq set, in increasing order of strictness. An empty mode means the
// caller has not chosen, and the driver applies the libpq default.
const (
	SSLDisable    SSLMode = "disable"
	SSLAllow      SSLMode = "allow"
	SSLPrefer     SSLMode = "prefer"
	SSLRequire    SSLMode = "require"
	SSLVerifyCA   SSLMode = "verify-ca"
	SSLVerifyFull SSLMode = "verify-full"
)

// SSLModes returns every accepted mode, in the order a form should offer them.
func SSLModes() []SSLMode {
	return []SSLMode{SSLDisable, SSLAllow, SSLPrefer, SSLRequire, SSLVerifyCA, SSLVerifyFull}
}

// TLS is the transport security of a connection: the mode and the files that
// verify_ca and verify-full need. The fields hold paths, never key material.
type TLS struct {
	Mode     SSLMode
	RootCert string
	Cert     string
	Key      string
}

// Config describes how to reach a PostgreSQL server.
//
// It is a value: copying it is how a connection is duplicated, and editing a
// copy never reaches the original — with the single exception of Params, which
// is a map, and which is why Clone exists.
//
// The Password field is the only secret this type carries, and it never leaves
// the process: String and LogValue redact it, so neither a %v in an error nor a
// logger that was handed the whole struct can print it.
type Config struct {
	// Name is what the user calls this connection. It has no effect on the
	// connection itself.
	Name string

	Host     string
	Port     int
	Database string
	User     string
	Password string

	TLS TLS

	// Params are session parameters sent on connect: application_name,
	// search_path, connect_timeout and the like.
	Params map[string]string

	// Archived hides the connection from the usual listing without deleting
	// it. Archiving is reversible; deleting is not.
	Archived bool
}

// Clone returns a copy that can be edited without touching the original.
//
// A plain assignment almost does this, and that "almost" is the bug: the two
// copies would share one Params map, so adding a session parameter to a
// duplicated connection would silently add it to the connection it came from.
func (c Config) Clone() Config {
	copied := c

	if c.Params != nil {
		copied.Params = make(map[string]string, len(c.Params))
		for key, value := range c.Params {
			copied.Params[key] = value
		}
	}

	return copied
}

// Validate reports whether the configuration can be handed to a driver.
//
// The error never carries the password: it is read by whoever typed the form
// and written to a log, and neither is a place for a secret.
func (c Config) Validate() error {
	switch {
	case strings.TrimSpace(c.Host) == "":
		return fmt.Errorf("%w: no host", ErrInvalidConfig)
	case c.Port < 1 || c.Port > 65535:
		return fmt.Errorf("%w: port %d is outside 1-65535", ErrInvalidConfig, c.Port)
	case strings.TrimSpace(c.Database) == "":
		return fmt.Errorf("%w: no database", ErrInvalidConfig)
	case strings.TrimSpace(c.User) == "":
		return fmt.Errorf("%w: no user", ErrInvalidConfig)
	}

	if err := c.TLS.Mode.validate(); err != nil {
		return err
	}

	return nil
}

// validate accepts the empty mode: not choosing is different from choosing
// something that does not exist, and the driver has a default for the former.
func (m SSLMode) validate() error {
	if m == "" {
		return nil
	}

	for _, known := range SSLModes() {
		if m == known {
			return nil
		}
	}

	return fmt.Errorf("%w: sslmode %q is not one of %v", ErrInvalidConfig, m, SSLModes())
}

// String renders the connection without its password.
func (c Config) String() string {
	mode := string(c.TLS.Mode)
	if mode == "" {
		mode = "default"
	}

	return fmt.Sprintf("%s@%s:%d/%s (sslmode=%s)", c.User, c.Host, c.Port, c.Database, mode)
}

// LogValue is what a structured logger prints for this type.
//
// Without it, handing a Config to a logger prints every field, password
// included. With it, the secret is unreachable through logging by construction
// rather than by everyone remembering to redact at each call site.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", c.Name),
		slog.String("host", c.Host),
		slog.Int("port", c.Port),
		slog.String("database", c.Database),
		slog.String("user", c.User),
		slog.String("sslmode", string(c.TLS.Mode)),
		slog.Bool("archived", c.Archived),
	)
}
