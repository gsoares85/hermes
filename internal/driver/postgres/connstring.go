package postgres

import (
	"fmt"
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
