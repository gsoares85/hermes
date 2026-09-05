package system

import "github.com/gsoares85/hermes/internal/core/secret"

// probe is a reference nothing ever stores a secret under.
//
// Reading it is how the vault of a platform finds out whether the store is
// there at all, and it is a read rather than a write on purpose: asking a
// question must not leave an entry behind in the keychain of someone who only
// opened the application. A store that is present answers "not found", and one
// that is absent answers something else.
var probe = secret.Ref{Service: secret.Service, Account: "availability-probe"}
