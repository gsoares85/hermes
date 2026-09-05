package conn

import (
	"slices"
	"strings"
)

// Connection keywords libpq understands, as opposed to session parameters.
//
// The distinction is not cosmetic. A session parameter is a GUC sent to the
// server in the startup message; a connection keyword configures the client and
// must never leave this machine. Treating the second as the first sends
// settings to a server that has no business with them, and — worse — throws
// away the ones that protect the connection: require_auth and channel_binding
// are precisely how libpq refuses a server that tries to downgrade the
// authentication method.
//
// pgx keeps this list too, and applies it to the settings it parses. We reach
// past that by writing RuntimeParams directly, so we have to keep it as well.
var connectionKeywords = map[string]bool{
	"connect_timeout":           true,
	"channel_binding":           true,
	"require_auth":              true,
	"target_session_attrs":      true,
	"passfile":                  true,
	"service":                   true,
	"servicefile":               true,
	"sslsni":                    true,
	"sslnegotiation":            true,
	"krbsrvname":                true,
	"krbspn":                    true,
	"min_protocol_version":      true,
	"max_protocol_version":      true,
	"gssencmode":                true,
	"gsslib":                    true,
	"gssdelegation":             true,
	"load_balance_hosts":        true,
	"fallback_application_name": true,
	"keepalives":                true,
	"keepalives_idle":           true,
	"keepalives_interval":       true,
	"keepalives_count":          true,
	"tcp_user_timeout":          true,
	"replication":               true,
	"options":                   true,
	"client_encoding":           true,
}

// Keywords the model handles through its own fields, so a duplicate arriving in
// a query string would be a second, conflicting source of truth.
var modelledKeywords = map[string]bool{
	"host": true, "hostaddr": true, "port": true, "dbname": true,
	"user": true, "password": true,
	"sslmode": true, "sslrootcert": true, "sslcert": true, "sslkey": true,
}

// Keywords carrying a secret that this version cannot handle safely.
//
// sslpassword is the passphrase of the client private key. pgx needs it inside
// the connection string to decrypt the key at parse time, and this package
// builds that string without secrets on purpose. Rather than break that rule
// quietly, an encrypted client key is refused until there is somewhere safe to
// keep the passphrase.
var unsupportedKeywords = map[string]string{
	"sslpassword": "encrypted client keys are not supported yet",
}

// classifyKeyword sorts a query key into how it must be carried.
func classifyKeyword(key string) keywordKind {
	switch {
	case modelledKeywords[key]:
		return keywordModelled
	case unsupportedKeywords[key] != "":
		return keywordUnsupported
	case connectionKeywords[key]:
		return keywordConnection
	default:
		return keywordSession
	}
}

type keywordKind int

const (
	// keywordSession is a GUC: it belongs in the startup message.
	keywordSession keywordKind = iota
	// keywordConnection configures the client and must reach the driver as a
	// connection setting, never as a runtime parameter.
	keywordConnection
	// keywordModelled already has a field on Config.
	keywordModelled
	// keywordUnsupported is refused rather than dropped.
	keywordUnsupported
)

// Keywords that carry a secret, whichever free-form map they arrive in.
//
// Params and Options are maps of text, so the guarantee that a saved connection
// holds no password is not a property of the types it is made of: password is a
// legitimate libpq keyword, and nothing stopped it being written under either
// map. This is the list that stops it, and it names every spelling libpq or its
// environment honours rather than only the obvious one.
var credentialKeywords = map[string]bool{
	"password":    true,
	"sslpassword": true,
	"pgpassword":  true,
}

// CredentialKeyword answers the first key of these settings that carries a
// secret, and whether there was one.
//
// The comparison is trimmed and folded because the file is written by hand:
// " Password " is the same keyword to anyone reading it, and a rule that only
// catches the tidy spelling catches only the honest mistake. The keys are
// sorted so that a file with two of them always names the same one, which is
// what keeps the message worth asserting on.
func CredentialKeyword(settings map[string]string) (string, bool) {
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	for _, key := range keys {
		if credentialKeywords[strings.ToLower(strings.TrimSpace(key))] {
			return key, true
		}
	}

	return "", false
}
