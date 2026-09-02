package postgres

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/gsoares85/hermes/internal/driver"
)

// The six modes libpq accepts. The empty string means the caller did not
// choose, and libpq's own default applies.
var knownSSLModes = map[string]bool{
	"disable": true, "allow": true, "prefer": true,
	"require": true, "verify-ca": true, "verify-full": true,
}

// Connection keywords libpq defines and pgx does not implement.
//
// pgx keeps its own list of the keywords it understands and turns every other
// setting in the string into a runtime parameter — a GUC sent to the server in
// the startup message. For a client-side keyword that is wrong twice over: the
// setting is not applied, and the server answers "unrecognized configuration
// parameter", which is a baffling reply to a DSN libpq itself would accept.
//
// Refused rather than dropped, because the difference matters most exactly
// where it is least visible: a keepalive nobody set up is an annoyance, while a
// gssencmode that looked honoured and did nothing is a connection protected
// less than it was asked to be.
//
// The three keywords not listed here — replication, options and
// client_encoding — are genuine startup-message parameters, so a runtime
// parameter is where they belong.
var unsupportedKeywords = map[string]bool{
	"gssencmode":                true,
	"gsslib":                    true,
	"gssdelegation":             true,
	"load_balance_hosts":        true,
	"fallback_application_name": true,
	"tcp_user_timeout":          true,
	"keepalives":                true,
	"keepalives_idle":           true,
	"keepalives_interval":       true,
	"keepalives_count":          true,
}

// connString builds the keyword/value connection string handed to pgx.
//
// Going through a string rather than assembling a *tls.Config by hand is
// deliberate. The six sslmode values are not six flags: allow and prefer each
// describe an ordered pair of attempts with a fallback, and verify-ca and
// verify-full differ only in whether the hostname is checked. libpq's semantics
// are already implemented inside pgx, and reimplementing them here would mean
// owning that subtlety — in the one part of the connection layer where being
// subtly wrong means connecting with less protection than was asked for.
//
// The password is deliberately absent. It is set on the parsed configuration
// instead, so that no string carrying the secret exists to be quoted into a log
// or an error message.
func connString(target driver.Target) (string, error) {
	if target.SSLMode != "" && !knownSSLModes[target.SSLMode] {
		return "", fmt.Errorf("%w: sslmode %q is not one of the libpq modes", ErrUnsupported, target.SSLMode)
	}

	settings := []struct{ key, value string }{
		{"host", target.Host},
		{"port", strconv.Itoa(target.Port)},
		{"dbname", target.Database},
		{"user", target.User},
		{"sslmode", target.SSLMode},
		{"sslrootcert", target.RootCert},
		{"sslcert", target.Cert},
		{"sslkey", target.Key},
	}

	var parts []string
	for _, setting := range settings {
		if setting.value == "" {
			continue
		}
		parts = append(parts, setting.key+"="+quote(setting.value))
	}

	// Client-side options go through the same string, which is what puts them
	// in front of the driver's own handling instead of past it. Sorted so that
	// the string is the same for the same target, which is what makes it
	// comparable in a test and in a log.
	for _, key := range sortedKeys(target.Options) {
		if unsupportedKeywords[key] {
			return "", fmt.Errorf(
				"%w: %s is a libpq setting this driver does not implement, and it would be sent to the server as a parameter instead",
				ErrUnsupported, key)
		}
		parts = append(parts, key+"="+quote(target.Options[key]))
	}

	return strings.Join(parts, " "), nil
}

// quote wraps a value the way libpq expects: single quotes around it, with
// backslashes and single quotes escaped. Certificate paths hold spaces on both
// Windows and macOS by default, and an unquoted one would end the value early
// and turn the rest of the path into keys nobody asked for.
func quote(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `\'`)

	return "'" + escaped + "'"
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	return keys
}
