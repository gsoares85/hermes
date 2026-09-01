package conn

import (
	"net/url"
	"regexp"
	"strings"
)

// Placeholder left where a password was. It is fixed rather than derived from
// the secret, so that the length of the original never leaks either.
const redacted = "xxxxx"

// Schemes a connection URI uses. Checked before the text is treated as a URI at
// all: url.Parse reads "auth:" in "auth: failed to connect" as a scheme, so any
// message beginning with a word and a colon used to be mistaken for a DSN and
// skipped the keyword redaction below entirely.
var uriScheme = regexp.MustCompile(`(?i)^postgres(ql)?$`)

// A connection URI embedded in a larger message, which is the shape a driver
// error actually has: the DSN it was given, wrapped in a sentence.
var embeddedURI = regexp.MustCompile(`(?i)(postgres(?:ql)?://[^\s:@/]+):[^\s@]*@`)

// Passwords in a keyword connection string, quoted or bare.
//
// Each alternative consumes a backslash together with whatever follows it,
// because that is how a value escapes a quote, a backslash or a space. Matching
// the quote alone would end the value early and leave the rest of the password —
// the part after the escape — sitting in the output next to the placeholder.
// The bare alternative stops at a query separator as well as at a space. It
// used to run to the next space, so "password=x&sslmode=require" collapsed to
// "password=xxxxx" — the secret went and the most useful part of the message
// went with it.
var passwordKeyword = regexp.MustCompile(
	`(?i)(password\s*=\s*)('(?:\\.|[^'\\])*'|"(?:\\.|[^"\\])*"|(?:\\.|[^\s&;])+)`)

// Passwords in a JSON object, which is the shape the frontend boundary uses:
// the window serialises its form, so a message quoting one carries the secret
// as "password":"…" rather than as password=….
var passwordJSON = regexp.MustCompile(`(?i)("(?:ssl)?password"\s*:\s*)"(?:\\.|[^"\\])*"`)

// Redact returns the connection string with its password replaced, in both the
// URL and the keyword form.
//
// A connection string reaches a log, an error message and a progress report,
// and every one of those is read by someone who should not learn the password.
// Redacting is therefore not a courtesy: it is the only form in which a DSN is
// allowed to leave this package.
func Redact(text string) string {
	trimmed := strings.TrimSpace(text)

	// Only a text that is nothing but a URI goes through the parser, which is
	// the path that also reaches a password carried as a query parameter.
	//
	// The space is what tells the two apart. A message beginning with a URI —
	// which is what a driver error looks like — used to take this path as well,
	// and url.Parse swallowed the rest of the sentence into the path and handed
	// it back percent-encoded: the secret went, and the message with it.
	if !strings.ContainsAny(trimmed, " \t\n") {
		if parsed, err := url.Parse(trimmed); err == nil && uriScheme.MatchString(parsed.Scheme) {
			return redactURL(parsed)
		}
	}

	redactedText := embeddedURI.ReplaceAllString(text, "${1}:"+redacted+"@")
	redactedText = passwordJSON.ReplaceAllString(redactedText, `${1}"`+redacted+`"`)

	return passwordKeyword.ReplaceAllString(redactedText, "${1}"+redacted)
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
