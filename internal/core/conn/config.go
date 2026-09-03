package conn

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// ErrInvalidConfig is returned by Validate for a connection that cannot be used.
var ErrInvalidConfig = errors.New("invalid connection")

// InvalidField is what Validate returns, and it names the field the rule is
// about as well as stating the rule.
//
// The name exists so that a caller reading a connection out of a file can point
// at the line the field is on. Matching the message with a string would be the
// alternative, and it would break the first time one of these sentences is
// reworded — the sort of coupling that survives review and fails in front of
// someone trying to fix their own file.
type InvalidField struct {
	// Field is the name of the offending field, spelled the way the
	// connections file spells it: host, port, user, sslmode.
	Field string
	// Problem states what is wrong, without ever quoting a secret.
	Problem string
}

func (e InvalidField) Error() string {
	return fmt.Sprintf("%s: %s", ErrInvalidConfig, e.Problem)
}

// Unwrap keeps errors.Is(err, ErrInvalidConfig) true for every caller that was
// written before the field had a name.
func (e InvalidField) Unwrap() error { return ErrInvalidConfig }

// DefaultPort is the port PostgreSQL listens on unless told otherwise.
const DefaultPort = 5432

// MaintenanceDatabase is what a connection opens when no database was named.
//
// Naming one should not be a precondition for looking: someone with a host and
// a password wants to see what is there. libpq would default to the user's own
// name, which is right far less often than postgres, and postgres exists on
// essentially every server precisely so that there is somewhere to connect to
// before you know what you are looking for.
const MaintenanceDatabase = "postgres"

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

// EffectiveDatabase is the database this configuration will actually open.
//
// It is separate from the field so that the field can stay empty: the
// difference between "no database was named" and "this database was named"
// decides what a failure to find it should say.
func (c Config) EffectiveDatabase() string {
	if strings.TrimSpace(c.Database) == "" {
		return MaintenanceDatabase
	}

	return c.Database
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
	// ID identifies this connection for as long as it exists, and is what the
	// password in the keychain is filed under. It is empty for a connection
	// that was never saved — one built from a URI to open something once — and
	// Validate does not require it, because a connection with no name and no
	// file behind it still connects.
	ID string

	// Name is what the user calls this connection. It has no effect on the
	// connection itself.
	Name string

	Host     string
	Port     int
	Database string
	User     string
	Password string

	TLS TLS

	// Params are session parameters sent to the server on connect: GUCs like
	// application_name, search_path or statement_timeout.
	Params map[string]string

	// Options are libpq connection keywords, which configure the client and
	// must never be sent to the server: connect_timeout, require_auth,
	// channel_binding and the like. They are kept apart from Params because
	// sending one as the other both loses the setting and, for the ones that
	// protect the connection, silently removes the protection.
	Options map[string]string

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

	copied.Params = copyOf(c.Params)
	copied.Options = copyOf(c.Options)

	return copied
}

func copyOf(original map[string]string) map[string]string {
	if original == nil {
		return nil
	}

	copied := make(map[string]string, len(original))
	for key, value := range original {
		copied[key] = value
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
		return InvalidField{Field: "host", Problem: "no host"}
	case c.Port < 1 || c.Port > 65535:
		return InvalidField{Field: "port", Problem: fmt.Sprintf("port %d is outside 1-65535", c.Port)}
	case strings.TrimSpace(c.User) == "":
		return InvalidField{Field: "user", Problem: "no user"}
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

	return InvalidField{
		Field:   "sslmode",
		Problem: fmt.Sprintf("sslmode %q is not one of %v", m, SSLModes()),
	}
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
		slog.String("id", c.ID),
		slog.String("name", c.Name),
		slog.String("host", c.Host),
		slog.Int("port", c.Port),
		slog.String("database", c.Database),
		slog.String("user", c.User),
		slog.String("sslmode", string(c.TLS.Mode)),
		slog.Bool("archived", c.Archived),
	)
}
