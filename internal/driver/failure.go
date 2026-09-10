package driver

import (
	"errors"
	"fmt"
)

// FailureClass is what went wrong with a connection attempt, in terms the layer
// above can act on.
//
// The engine knows why a connection failed; only the engine can read a SQLSTATE
// or tell a refused socket from a dropped one. What crosses the seam is this
// classification, not the driver's own error text, so that the message a person
// reads is written where the product speaks rather than where the protocol does.
type FailureClass string

// The seven classes a connection attempt is sorted into, plus the honest
// admission that it was none of them.
const (
	FailureUnknown FailureClass = "unknown"

	// FailureDNS is a host name that does not resolve.
	FailureDNS FailureClass = "dns"
	// FailureTimeout is no answer within the deadline, which usually means a
	// firewall swallowing packets rather than refusing them.
	FailureTimeout FailureClass = "timeout"
	// FailureRefused is a closed port: the address answered, and said no.
	FailureRefused FailureClass = "refused"
	// FailureTLS is a handshake that did not complete, whether because the
	// server does not offer TLS or because its certificate was not accepted.
	FailureTLS FailureClass = "tls"
	// FailureDropped is a connection that was established and then lost: the
	// server stopped, was restarted, or terminated the backend. It is not the
	// same as a refusal, which happens before anything is established, and it
	// is what a pooled connection sees first when a server goes away.
	FailureDropped FailureClass = "dropped"
	// FailureAuth is a server that answered and rejected the credentials.
	FailureAuth FailureClass = "auth"
	// FailureNotAuthorized is a server with no pg_hba rule for this user
	// coming from this address, which is a different fix from a wrong password.
	FailureNotAuthorized FailureClass = "not-authorized"
	// FailureMissingDatabase is a database name the server does not know.
	FailureMissingDatabase FailureClass = "missing-database"
	// FailureReadOnly is a statement the server refused because the
	// transaction cannot write. It is not a failure of the connection — the
	// connection is working — and it is the outcome a connection marked
	// read-only is supposed to produce, which is why it is classified rather
	// than left to the layer above to recognise from a message.
	FailureReadOnly FailureClass = "read-only"
)

// Failure is a classified connection failure.
//
// It keeps the driver's own error underneath so that a developer can still read
// it, while the layer above decides what a person is shown.
type Failure struct {
	Class FailureClass

	// SQLState is the five-character code the server answered with, empty when
	// the server never answered at all.
	SQLState string

	// Err is what the driver reported.
	Err error
}

func (f *Failure) Error() string {
	if f.SQLState != "" {
		return fmt.Sprintf("%s (SQLSTATE %s): %v", f.Class, f.SQLState, f.Err)
	}

	return fmt.Sprintf("%s: %v", f.Class, f.Err)
}

func (f *Failure) Unwrap() error {
	return f.Err
}

// ClassOf reports how a connection error was classified, and whether it was
// classified at all. An error from somewhere else comes back unknown.
func ClassOf(err error) (FailureClass, bool) {
	var failure *Failure
	if !errors.As(err, &failure) {
		return FailureUnknown, false
	}

	return failure.Class, true
}
