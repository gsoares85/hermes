package conn

import (
	"net/url"
	"regexp"
)

// Placeholder left where a password was. It is fixed rather than derived from
// the secret, so that the length of the original never leaks either.
const redacted = "xxxxx"

// Passwords in a keyword connection string, quoted or bare.
//
// Each alternative consumes a backslash together with whatever follows it,
// because that is how a value escapes a quote, a backslash or a space. Matching
// the quote alone would end the value early and leave the rest of the password —
// the part after the escape — sitting in the output next to the placeholder.
var passwordKeyword = regexp.MustCompile(
	`(?i)(password\s*=\s*)('(?:\\.|[^'\\])*'|"(?:\\.|[^"\\])*"|(?:\\.|\S)+)`)

// Redact returns the connection string with its password replaced, in both the
// URL and the keyword form.
//
// A connection string reaches a log, an error message and a progress report,
// and every one of those is read by someone who should not learn the password.
// Redacting is therefore not a courtesy: it is the only form in which a DSN is
// allowed to leave this package.
func Redact(dsn string) string {
	// A keyword string carries no scheme, which is what tells the two forms
	// apart: "host=localhost password=x" never parses as a URL.
	if parsed, err := url.Parse(dsn); err == nil && parsed.Scheme != "" {
		return redactURL(parsed)
	}

	return passwordKeyword.ReplaceAllString(dsn, "${1}"+redacted)
}

func redactURL(parsed *url.URL) string {
	if parsed.User != nil {
		if _, set := parsed.User.Password(); set {
			parsed.User = url.UserPassword(parsed.User.Username(), redacted)
		}
	}

	query := parsed.Query()
	if query.Has("password") {
		query.Set("password", redacted)
		parsed.RawQuery = query.Encode()
	}

	return parsed.String()
}
