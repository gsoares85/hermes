package postgres

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gsoares85/hermes/internal/driver"
)

// The server answered, so the SQLSTATE decides. These are the codes a
// connection attempt comes back with, and they are read from the code rather
// than from the message, which changes with the server locale and version.
func TestClassifyReadsTheSQLState(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		code    string
		message string
		want    driver.FailureClass
	}{
		"wrong password":      {"28P01", "password authentication failed for user \"hermes\"", driver.FailureAuth},
		"no such role":        {"28000", "role \"hermes\" does not exist", driver.FailureAuth},
		"no pg_hba rule":      {"28000", "no pg_hba.conf entry for host \"10.0.0.1\"", driver.FailureNotAuthorized},
		"pg_hba uppercase":    {"28000", "No pg_hba.conf entry for host", driver.FailureNotAuthorized},
		"database is missing": {"3D000", "database \"app\" does not exist", driver.FailureMissingDatabase},

		// A server on its way down says so before it closes the socket, and
		// the message arrives as an ordinary SQLSTATE. Reading it is what
		// tells a stopped server apart from one that was never reachable —
		// and without it the whole transport branch below is skipped, because
		// a server that answered is answered for here.
		"server shutting down":  {"57P01", "terminating connection due to administrator command", driver.FailureDropped},
		"backend terminated":    {"57P01", "terminating connection due to administrator command", driver.FailureDropped},
		"another backend crash": {"57P02", "terminating connection because of crash of another server process", driver.FailureDropped},

		// A statement refused because the transaction cannot write. It is the
		// code a connection Hermes marked read-only comes back with, and the
		// layer above turns it into the sentence saying where the mark is
		// cleared — which it can only do if the code is read here first.
		"read-only transaction": {"25006", "cannot execute INSERT in a read-only transaction", driver.FailureReadOnly},

		"anything else": {"53300", "too many connections", driver.FailureUnknown},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := classify(&pgconn.PgError{Code: tc.code, Message: tc.message})
			if got.Class != tc.want {
				t.Errorf("Class = %q, want %q", got.Class, tc.want)
			}
			if got.SQLState != tc.code {
				t.Errorf("SQLState = %q, want %q", got.SQLState, tc.code)
			}
		})
	}
}

// A wrapped error is the normal case: pgx returns its own error with the
// server's underneath.
func TestClassifyLooksThroughWrapping(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("failed to connect: %w", &pgconn.PgError{Code: "3D000", Message: "database does not exist"})

	if got := classify(wrapped); got.Class != driver.FailureMissingDatabase {
		t.Errorf("Class = %q, want %q", got.Class, driver.FailureMissingDatabase)
	}
}

// Nothing answered, so the transport is all there is to read.
func TestClassifyReadsTheTransport(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		err  error
		want driver.FailureClass
	}{
		"name does not resolve": {
			&net.DNSError{Err: "no such host", Name: "db.example.com", IsNotFound: true},
			driver.FailureDNS,
		},
		"port is closed": {
			fmt.Errorf("dial tcp: %w", syscall.ECONNREFUSED),
			driver.FailureRefused,
		},
		"deadline passed": {
			fmt.Errorf("connecting: %w", context.DeadlineExceeded),
			driver.FailureTimeout,
		},
		"certificate not trusted": {
			fmt.Errorf("tls: %w", x509.UnknownAuthorityError{}),
			driver.FailureTLS,
		},
		"certificate for another name": {
			fmt.Errorf("tls: %w", x509.HostnameError{Host: "127.0.0.1"}),
			driver.FailureTLS,
		},
		"server without tls": {
			errors.New("server refused TLS connection"),
			driver.FailureTLS,
		},
		"connection reset": {
			fmt.Errorf("read tcp: %w", syscall.ECONNRESET),
			driver.FailureDropped,
		},
		// A stopped server closes the socket while the client is waiting for
		// the next protocol message, and that arrives as an EOF rather than as
		// an error number.
		"eof mid protocol": {
			fmt.Errorf("failed to receive message: %w", io.ErrUnexpectedEOF),
			driver.FailureDropped,
		},
		"plain eof": {
			fmt.Errorf("reading: %w", io.EOF),
			driver.FailureDropped,
		},
		"nothing recognisable": {
			errors.New("something nobody predicted"),
			driver.FailureUnknown,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := classify(tc.err)
			if got.Class != tc.want {
				t.Errorf("Class = %q, want %q", got.Class, tc.want)
			}
			if got.SQLState != "" {
				t.Errorf("SQLState = %q, want empty: nothing answered", got.SQLState)
			}
		})
	}
}

// A timeout is what the caller sees when anything below takes long enough, so a
// more specific cause that is also present has to win.
func TestASpecificCauseBeatsTheDeadline(t *testing.T) {
	t.Parallel()

	refusedAndLate := fmt.Errorf("%w: %w", context.DeadlineExceeded, syscall.ECONNREFUSED)

	if got := classify(refusedAndLate); got.Class != driver.FailureRefused {
		t.Errorf("Class = %q, want %q: a refusal explains more than a deadline", got.Class, driver.FailureRefused)
	}
}

// The error a closed port actually produces, rather than a synthetic one. It is
// the case where the portable error number is not what the kernel returns on
// every platform, and where the message cannot be matched because Windows
// translates it into the system language.
func TestClassifyARealRefusedConnection(t *testing.T) {
	t.Parallel()

	// Port 1 is reserved and nothing listens on it.
	_, err := net.Dial("tcp", "127.0.0.1:1")
	if err == nil {
		t.Skip("something is listening on port 1")
	}

	if got := classify(err); got.Class != driver.FailureRefused {
		t.Errorf("Class = %q, want %q for %v", got.Class, driver.FailureRefused, err)
	}
}

func TestClassifyOfNoErrorIsNothing(t *testing.T) {
	t.Parallel()

	if got := classify(nil); got != nil {
		t.Errorf("classify(nil) = %v, want nil", got)
	}
}

// The failure has to keep the driver error underneath: the class is what a
// person reads, and the original is what a developer needs.
func TestFailureKeepsTheOriginal(t *testing.T) {
	t.Parallel()

	original := &pgconn.PgError{Code: "28P01", Message: "password authentication failed"}
	failure := classify(fmt.Errorf("connecting: %w", original))

	var recovered *pgconn.PgError
	if !errors.As(failure, &recovered) {
		t.Fatal("the original server error cannot be recovered from the failure")
	}
	if recovered.Code != "28P01" {
		t.Errorf("recovered code = %q, want 28P01", recovered.Code)
	}
}
