package secret

import (
	"errors"
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

// Passwords in the way Go itself prints a value: {Host:h Password:s3cr3t},
// map[password:s3cr3t], and the %#v form with the value quoted.
//
// This is not a connection string notation at all — it is what fmt produces,
// and therefore what the slog handler produces when it renders an attribute it
// does not otherwise recognise. Without these two the net had a hole in the one
// format it generates itself: a struct with a password field, logged whole,
// came out with the password in it.
//
// The separator tells the two apart, and it is what keeps prose intact. A colon
// followed immediately by the value is how Go prints; a colon followed by a
// space is how English writes, so "Wrong password: check the spelling" is left
// alone. A quoted value is unambiguous either way and allows the space.
var (
	passwordQuotedField = regexp.MustCompile(
		`(?i)((?:ssl)?password\s*:\s*)('(?:\\.|[^'\\])*'|"(?:\\.|[^"\\])*")`)

	// The value stops at the delimiters that end a rendered field rather than
	// at a space alone: a password is the last thing before the closing brace
	// as often as not, and swallowing the brace would corrupt the message
	// around the secret rather than only removing the secret.
	passwordRenderedField = regexp.MustCompile(
		`(?i)((?:ssl)?password:)([^\s,}\])"';]+)`)
)

// Passwords in a JSON object, which is the shape the frontend boundary uses:
// the window serialises its form, so a message quoting one carries the secret
// as "password":"…" rather than as password=….
var passwordJSON = regexp.MustCompile(`(?i)("(?:ssl)?password"\s*:\s*)"(?:\\.|[^"\\])*"`)

// Query keys of a connection URI that carry a secret.
//
// sslpassword is the passphrase of the client private key, which is as much a
// secret as the password itself — the JSON form above already treats it as one,
// and a URI is the other shape it arrives in. The keyword form needs no entry:
// the pattern above matches the "password=" inside "sslpassword=" already.
var secretQueryKeys = []string{"password", "sslpassword"}

// Redact returns the connection string with its password replaced, in both the
// URL and the keyword form.
//
// A connection string reaches a log, an error message and a progress report,
// and every one of those is read by someone who should not learn the password.
// Redacting is therefore not a courtesy: it is the only form in which a DSN is
// allowed to leave the process.
//
// It lives beside the vault contract rather than beside the connection model
// because the callers are spread across the core: a diagnosis, a job report and
// an error crossing the window boundary all need it, and none of them has any
// other reason to know what a connection is.
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
	redactedText = passwordKeyword.ReplaceAllString(redactedText, "${1}"+redacted)

	// Quoted before bare: the quoted pattern consumes the quotes, which the
	// bare one would otherwise stop at, leaving the secret between them.
	redactedText = passwordQuotedField.ReplaceAllString(redactedText, "${1}"+redacted)

	return passwordRenderedField.ReplaceAllString(redactedText, "${1}"+redacted)
}

func redactURL(parsed *url.URL) string {
	if parsed.User != nil {
		if _, set := parsed.User.Password(); set {
			parsed.User = url.UserPassword(parsed.User.Username(), redacted)
		}
	}

	query := parsed.Query()
	replaced := false
	for _, key := range secretQueryKeys {
		if query.Has(key) {
			query.Set(key, redacted)
			replaced = true
		}
	}
	if replaced {
		parsed.RawQuery = query.Encode()
	}

	return parsed.String()
}

// Error is the last thing an error crosses on its way out of the process — to
// the window, to a report, to a terminal.
//
// It answers a plain error carrying the redacted text and nothing else. The
// chain is dropped on purpose: an error that can still be unwrapped answers the
// original message when anyone asks it to, and a redaction anyone can undo is
// not one. Callers that need to tell one failure from another match their
// sentinel before crossing the boundary, which is the side of it where the
// distinction still means something.
func Error(err error) error {
	if err == nil {
		return nil
	}

	return errors.New(Redact(err.Error()))
}
