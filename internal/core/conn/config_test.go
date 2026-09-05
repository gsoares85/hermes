package conn_test

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
)

func sample() conn.Config {
	return conn.Config{
		Name:     "staging",
		Host:     "db.example.com",
		Port:     5432,
		Database: "hermes",
		User:     "hermes",
		Password: "s3cr3t",
		TLS:      conn.TLS{Mode: conn.SSLRequire},
		Params:   map[string]string{"application_name": "hermes"},
	}
}

// A duplicated connection is edited on its own. Sharing the parameter map with
// the original would make editing the copy change the original, which is the
// one bug a value type is supposed to make impossible.
func TestCloneDoesNotShareState(t *testing.T) {
	t.Parallel()

	original := sample()
	copied := original.Clone()

	copied.Name = "production"
	copied.Params["application_name"] = "other"
	copied.Params["search_path"] = "public"

	if copied.Name != "production" {
		t.Errorf("copy name = %q, want the edit to have taken", copied.Name)
	}
	if original.Name != "staging" {
		t.Errorf("original name = %q, want it untouched", original.Name)
	}
	if got := original.Params["application_name"]; got != "hermes" {
		t.Errorf("original param = %q, want it untouched", got)
	}
	if _, added := original.Params["search_path"]; added {
		t.Error("a parameter added to the copy appeared in the original")
	}
}

func TestCloneOfAConfigWithoutParams(t *testing.T) {
	t.Parallel()

	original := conn.Config{Host: "localhost", Port: 5432, Database: "hermes", User: "hermes"}
	copied := original.Clone()

	// Not an empty map: a connection with no session parameters and one with
	// an empty set of them are the same thing, and inventing a map here would
	// make them serialise differently later.
	if copied.Params != nil {
		t.Errorf("copy params = %v, want nil", copied.Params)
	}
	if original.Params != nil {
		t.Errorf("original params = %v, want nil", original.Params)
	}
}

// Archiving is a state of the connection, not a deletion: the definition stays
// readable so it can be brought back.
func TestArchivedIsCarriedByTheCopy(t *testing.T) {
	t.Parallel()

	original := sample()
	original.Archived = true

	if !original.Clone().Archived {
		t.Error("the copy of an archived connection is not archived")
	}
}

// The password must not be reachable through any rendering of the config. This
// is the assertion that matters, so every path that a careless %v or a logger
// could take is checked at once.
func TestNoRenderingLeaksThePassword(t *testing.T) {
	t.Parallel()

	config := sample()

	// The last one is the case that actually bites: a Config sitting inside
	// another struct that someone prints whole.
	enclosing := struct {
		ID     int
		Config conn.Config
	}{ID: 1, Config: config}

	renderings := map[string]string{
		"String":          config.String(),
		"%v":              fmt.Sprintf("%v", config),
		"%+v":             fmt.Sprintf("%+v", config),
		"slog value":      config.LogValue().String(),
		"inside a struct": fmt.Sprintf("%+v", enclosing),
	}

	for name, rendered := range renderings {
		if strings.Contains(rendered, "s3cr3t") {
			t.Errorf("%s leaked the password: %s", name, rendered)
		}
	}
}

func TestRenderingKeepsWhatIsNotSecret(t *testing.T) {
	t.Parallel()

	rendered := sample().String()
	for _, want := range []string{"db.example.com", "5432", "hermes", "require"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("String() = %q, want it to carry %q", rendered, want)
		}
	}
}

// A logger reaches LogValue through the slog.LogValuer interface, and only if
// the method is on the type the caller actually passes.
func TestConfigIsALogValuer(t *testing.T) {
	t.Parallel()

	var value any = sample()
	if _, ok := value.(slog.LogValuer); !ok {
		t.Fatal("Config does not implement slog.LogValuer, so a logger would print its fields")
	}
}

func TestValidateAcceptsAUsableConfig(t *testing.T) {
	t.Parallel()

	for name, config := range map[string]conn.Config{
		"full":            sample(),
		"no password":     {Host: "localhost", Port: 5432, Database: "hermes", User: "hermes"},
		"no tls mode set": {Host: "localhost", Port: 5432, Database: "hermes", User: "hermes"},
		"ipv6 host":       {Host: "::1", Port: 5432, Database: "hermes", User: "hermes"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := config.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

// Naming a database must not be a precondition for connecting: someone with a
// host and a password wants to see what is there before deciding.
func TestADatabaseIsOptional(t *testing.T) {
	t.Parallel()

	config := conn.Config{Host: "localhost", Port: 5432, User: "hermes"}

	if err := config.Validate(); err != nil {
		t.Errorf("Validate() = %v, want a connection without a database to be valid", err)
	}
	if got := config.EffectiveDatabase(); got != conn.MaintenanceDatabase {
		t.Errorf("EffectiveDatabase() = %q, want %q", got, conn.MaintenanceDatabase)
	}
	if got := config.Target().Database; got != conn.MaintenanceDatabase {
		t.Errorf("the target opens %q, want %q", got, conn.MaintenanceDatabase)
	}
	// The field stays empty, because the difference between "none was named"
	// and "this one was named" changes what a failure should say.
	if config.Database != "" {
		t.Errorf("Database = %q, want the field left as the user left it", config.Database)
	}
}

func TestANamedDatabaseIsUsedAsGiven(t *testing.T) {
	t.Parallel()

	config := conn.Config{Host: "localhost", Port: 5432, User: "hermes", Database: "app"}

	if got := config.EffectiveDatabase(); got != "app" {
		t.Errorf("EffectiveDatabase() = %q, want app", got)
	}
}

func TestValidateRejectsWhatCannotConnect(t *testing.T) {
	t.Parallel()

	base := sample()

	cases := map[string]func(*conn.Config){
		"no host":       func(c *conn.Config) { c.Host = "" },
		"port zero":     func(c *conn.Config) { c.Port = 0 },
		"port negative": func(c *conn.Config) { c.Port = -1 },
		"port too high": func(c *conn.Config) { c.Port = 70000 },
		"no user":       func(c *conn.Config) { c.User = "" },
		"unknown mode":  func(c *conn.Config) { c.TLS.Mode = "sometimes" },
	}

	for name, break_ := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			config := base.Clone()
			break_(&config)

			if err := config.Validate(); !errors.Is(err, conn.ErrInvalidConfig) {
				t.Errorf("Validate() = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

// An error about a bad configuration is shown to the user and written to a log,
// so it is one of the places a password most easily escapes.
func TestValidationErrorDoesNotLeakThePassword(t *testing.T) {
	t.Parallel()

	config := sample()
	config.Host = ""

	err := config.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error")
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("the error leaked the password: %v", err)
	}
}

func TestSSLModesAreTheLibpqSet(t *testing.T) {
	t.Parallel()

	want := []conn.SSLMode{
		conn.SSLDisable, conn.SSLAllow, conn.SSLPrefer,
		conn.SSLRequire, conn.SSLVerifyCA, conn.SSLVerifyFull,
	}
	if got := conn.SSLModes(); len(got) != len(want) {
		t.Fatalf("SSLModes() has %d entries, want %d: %v", len(got), len(want), got)
	}

	for _, mode := range want {
		config := conn.Config{Host: "h", Port: 5432, Database: "d", User: "u", TLS: conn.TLS{Mode: mode}}
		if err := config.Validate(); err != nil {
			t.Errorf("mode %q rejected by Validate: %v", mode, err)
		}
	}
}

// Params and Options are maps of text, so the promise that a saved connection
// carries no password is not something the type can keep on its own: password
// is a keyword libpq honours, and one written under either map reaches the
// connections file in plain text and the connection string after it.
//
// It is refused rather than dropped. Someone who typed it there believes it is
// taking effect, and a setting silently removed is a connection that fails to
// authenticate for a reason nothing on screen explains.
func TestASecretUnderAFreeFormKeyIsNotAValidConnection(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		params, options map[string]string
		field           string
	}{
		"password in params":     {params: map[string]string{"password": "s3cr3t"}, field: "params.password"},
		"password in options":    {options: map[string]string{"password": "s3cr3t"}, field: "options.password"},
		"sslpassword in options": {options: map[string]string{"sslpassword": "s3cr3t"}, field: "options.sslpassword"},
		"pgpassword in params":   {params: map[string]string{"pgpassword": "s3cr3t"}, field: "params.pgpassword"},
		"spelled loudly":         {params: map[string]string{" PASSWORD ": "s3cr3t"}, field: "params. PASSWORD "},
	}

	for name, carrying := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			config := sample()
			config.Params, config.Options = carrying.params, carrying.options

			err := config.Validate()
			if !errors.Is(err, conn.ErrInvalidConfig) {
				t.Fatalf("Validate() = %v, want an invalid connection", err)
			}

			var invalid conn.InvalidField
			if !errors.As(err, &invalid) {
				t.Fatalf("Validate() = %v, want it to name the field", err)
			}
			if invalid.Field != carrying.field {
				t.Errorf("Validate() blamed %q, want %q", invalid.Field, carrying.field)
			}
			if strings.Contains(err.Error(), "s3cr3t") {
				t.Errorf("the refusal quotes the secret: %v", err)
			}
		})
	}
}

// The settings that are not secrets stay welcome. A rule that refused the whole
// map would be a rule nobody could use application_name or connect_timeout
// under.
func TestOrdinarySettingsAreStillValid(t *testing.T) {
	t.Parallel()

	config := sample()
	config.Params = map[string]string{"application_name": "hermes", "search_path": "public"}
	config.Options = map[string]string{"connect_timeout": "10", "require_auth": "scram-sha-256"}

	if err := config.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}
