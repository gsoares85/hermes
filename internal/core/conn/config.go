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

// Environment is what a connection is: somebody's laptop, a staging server, or
// the one that must not be broken.
//
// It is a closed set rather than free text because the window draws it, and a
// mark drawn from free text is a mark that quietly disappears the day somebody
// types "prd". What it never is, is a rule: naming a connection production
// changes nothing about how it connects — it changes what the person looking at
// the window can see about where they are.
type Environment string

// The three environments, from the one where a mistake costs nothing to the one
// where it costs the most. An empty environment means the connection was never
// labelled, which is the ordinary case and not a fourth value.
const (
	EnvironmentDevelopment Environment = "dev"
	EnvironmentStaging     Environment = "staging"
	EnvironmentProduction  Environment = "prod"
)

// Environments returns every label, in the order a form should offer them.
func Environments() []Environment {
	return []Environment{EnvironmentDevelopment, EnvironmentStaging, EnvironmentProduction}
}

// validate accepts the empty label, for the same reason the empty mode is
// accepted: not labelling a connection is different from labelling it something
// that does not exist.
func (e Environment) validate() error {
	if e == "" {
		return nil
	}

	for _, known := range Environments() {
		if e == known {
			return nil
		}
	}

	return InvalidField{
		Field:   "environment",
		Problem: fmt.Sprintf("environment %q is not one of %v", e, Environments()),
	}
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

	// Environment is what this connection is: development, staging or
	// production. It changes nothing about how the connection is made and
	// everything about how it is drawn.
	Environment Environment

	// ReadOnly asks the server to refuse every statement that writes.
	//
	// It is a request made once, on connect, and honoured by the server for
	// the life of the connection — see Target. Deciding here whether a
	// statement writes would mean parsing SQL, and would be wrong about the
	// first function that writes inside itself.
	ReadOnly bool

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

	if err := c.Environment.validate(); err != nil {
		return err
	}

	// The two free-form maps are where the promise that this format holds no
	// password would otherwise end. Params and Options are maps of text, and
	// password is a keyword libpq honours, so without this a secret written
	// under either one is saved to the connections file in plain text and
	// emitted into the connection string afterwards. Refused rather than
	// dropped: someone who typed it there believes it is taking effect, and
	// has to be told where passwords actually live.
	if err := c.noCredentials("params", c.Params); err != nil {
		return err
	}

	if err := c.noCredentials("options", c.Options); err != nil {
		return err
	}

	return c.oneOpinionOnWriting()
}

// oneOpinionOnWriting refuses a connection that says two things about whether
// it may write.
//
// Params and Options are free-form text that the person types, the file keeps
// and the server is handed, so both can carry a second opinion about the one
// setting the read-only mark exists to state: the parameter itself, or a libpq
// options string carrying -c default_transaction_read_only=off. As it happens
// the server applies them in the order that keeps the mark winning, which is an
// internal detail of PostgreSQL that nothing here fixes and nobody should have
// to know to trust the mark.
//
// Refused rather than quietly overridden in either direction. Someone who typed
// it believes it is taking effect, and a protection that depends on which of
// two settings the server reads first is not a protection.
func (c Config) oneOpinionOnWriting() error {
	if !c.ReadOnly {
		return nil
	}

	for key := range c.Params {
		if strings.EqualFold(strings.TrimSpace(key), readOnlyParam) {
			return InvalidField{
				Field: "params." + key,
				Problem: fmt.Sprintf(
					"this connection is marked read-only, so params.%s would be a second answer to the same question — clear one of them",
					key),
			}
		}
	}

	for key, value := range c.Options {
		if !strings.Contains(strings.ToLower(value), readOnlyParam) {
			continue
		}

		return InvalidField{
			Field: "options." + key,
			Problem: fmt.Sprintf(
				"this connection is marked read-only, so options.%s must not set %s as well — clear one of them",
				key, readOnlyParam),
		}
	}

	return nil
}

// noCredentials refuses a secret smuggled in under a free-form key.
func (c Config) noCredentials(field string, settings map[string]string) error {
	key, found := CredentialKeyword(settings)
	if !found {
		return nil
	}

	return InvalidField{
		Field: field + "." + key,
		Problem: fmt.Sprintf(
			"%s.%s would put a password in the connections file in plain text — Hermes keeps passwords in the keychain of the system",
			field, key),
	}
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
		slog.String("environment", string(c.Environment)),
		slog.Bool("readonly", c.ReadOnly),
		slog.Bool("archived", c.Archived),
	)
}
