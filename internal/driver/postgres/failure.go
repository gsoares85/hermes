package postgres

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gsoares85/hermes/internal/driver"
)

// SQLSTATE codes a connection attempt can come back with.
//
// This is the single table the note on the task asks for: every layer that has
// to tell one server refusal from another reads it here rather than matching on
// message text, which changes with the server locale and the server version.
const (
	sqlStateInvalidPassword      = "28P01"
	sqlStateInvalidAuthorization = "28000"
	sqlStateInvalidCatalog       = "3D000"

	// A server on its way down says so before it closes the socket. Both
	// codes mean the connection that existed is gone: 57P01 is a shutdown or
	// an administrator terminating the backend, 57P02 is another backend
	// crashing and taking the cluster down with it.
	sqlStateAdminShutdown = "57P01"
	sqlStateCrashShutdown = "57P02"
)

// classify sorts a driver error into one of the classes the layer above knows
// how to explain.
//
// The order matters. A server that answered is asked first, because a SQLSTATE
// is a definite statement about what went wrong; only when nothing answered is
// the transport inspected, where the evidence is weaker.
func classify(err error) *driver.Failure {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return &driver.Failure{Class: fromSQLState(pgErr), SQLState: pgErr.Code, Err: err}
	}

	return &driver.Failure{Class: fromTransport(err), Err: err}
}

// fromSQLState reads the code the server answered with.
//
// 28000 covers both a rejected role and a connection no pg_hba rule allows, and
// the two are fixed in different places by different people, so the message is
// consulted for that one code. It is the only place message text is read, and
// only to split a code that is genuinely ambiguous.
//
// A dropped connection has to be recognised here as well as in the transport,
// and which of the two sees it is a race. A server being stopped sends a FATAL
// before closing the socket: read the message first and this is a SQLSTATE,
// miss it and the same event arrives below as an EOF. classify answers a server
// that spoke without consulting the transport at all, so a code missing from
// this table is not caught further down — it is reported as unknown, which for
// a stopped server is the one answer that helps nobody.
func fromSQLState(pgErr *pgconn.PgError) driver.FailureClass {
	switch pgErr.Code {
	case sqlStateInvalidPassword:
		return driver.FailureAuth

	case sqlStateInvalidAuthorization:
		if strings.Contains(strings.ToLower(pgErr.Message), "pg_hba") {
			return driver.FailureNotAuthorized
		}

		return driver.FailureAuth

	case sqlStateInvalidCatalog:
		return driver.FailureMissingDatabase

	case sqlStateAdminShutdown, sqlStateCrashShutdown:
		return driver.FailureDropped

	default:
		return driver.FailureUnknown
	}
}

// fromTransport reads what happened below the protocol, where nothing answered.
func fromTransport(err error) driver.FailureClass {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return driver.FailureDNS
	}

	if isTLS(err) {
		return driver.FailureTLS
	}

	if isRefused(err) {
		return driver.FailureRefused
	}

	// After a refusal, because a connection that was never established cannot
	// have been dropped. An EOF in the middle of the protocol counts: the
	// socket closed cleanly while a message was expected, which is what a
	// server being stopped looks like from this side, and it arrives as often
	// as the reset does depending on when the probe lands.
	if isDropped(err) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return driver.FailureDropped
	}

	// Checked after the causes above because a deadline is what a caller sees
	// when any of them takes long enough, and the specific reason is the more
	// useful one.
	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		return driver.FailureTimeout
	}

	return driver.FailureUnknown
}

func isTLS(err error) bool {
	var (
		unknownAuthority x509.UnknownAuthorityError
		hostname         x509.HostnameError
		invalid          x509.CertificateInvalidError
		recordHeader     tls.RecordHeaderError
		alert            *tls.CertificateVerificationError
	)

	switch {
	case errors.As(err, &unknownAuthority),
		errors.As(err, &hostname),
		errors.As(err, &invalid),
		errors.As(err, &recordHeader),
		errors.As(err, &alert):
		return true
	}

	// pgx reports a server built without TLS as a refusal of the SSL request
	// rather than as a handshake error, so there is no typed error to match.
	return strings.Contains(strings.ToLower(err.Error()), "server refused tls")
}

func isTimeout(err error) bool {
	var netErr net.Error

	return errors.As(err, &netErr) && netErr.Timeout()
}
