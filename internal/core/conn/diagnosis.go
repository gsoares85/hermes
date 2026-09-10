package conn

import (
	"fmt"
	"strings"

	"github.com/gsoares85/hermes/internal/core/secret"
	"github.com/gsoares85/hermes/internal/driver"
)

// Diagnosis is a connection failure explained to the person who hit it.
//
// A raw driver message on the first attempt is where people give up, so every
// class the engine can report is turned into three things: what happened, why
// it probably happened, and what to do next. The driver's own text is kept in
// Detail for whoever can read it, and never leads.
type Diagnosis struct {
	Class    driver.FailureClass
	Summary  string
	Cause    string
	NextStep string

	// Detail is the underlying error, redacted. It is for a bug report, not
	// for the first thing a user sees.
	Detail string
}

// Failed reports whether there was anything to diagnose.
func (d Diagnosis) Failed() bool {
	return d.Summary != ""
}

// String renders the diagnosis the way a log line carries it.
func (d Diagnosis) String() string {
	if !d.Failed() {
		return "connected"
	}

	return fmt.Sprintf("%s %s %s", d.Summary, d.Cause, d.NextStep)
}

// Diagnose turns a connection failure into something actionable.
//
// It takes the configuration as well as the error because a useful message
// names the host that did not resolve and the database that does not exist. A
// message that only says what category of thing went wrong leaves the reader
// exactly where they started.
func Diagnose(err error, config Config) Diagnosis {
	if err == nil {
		return Diagnosis{}
	}

	class, _ := driver.ClassOf(err)

	diagnosis := explain(class, config)
	diagnosis.Class = class
	// Redacted, because a driver that echoes the connection string it was
	// given is exactly how a password reaches a bug report.
	diagnosis.Detail = secret.Redact(err.Error())

	return diagnosis
}

// explain holds the table this task exists for. Each entry names the thing that
// is wrong, not the category it belongs to, and each next step points at a
// different place — a wrong password and a missing pg_hba rule are fixed by
// different people in different files.
func explain(class driver.FailureClass, c Config) Diagnosis {
	address := fmt.Sprintf("%s:%d", c.Host, c.Port)

	switch class {
	case driver.FailureDNS:
		return Diagnosis{
			Summary:  fmt.Sprintf("The host name %q could not be resolved.", c.Host),
			Cause:    "The name is misspelled, or the DNS server this machine uses does not know it — a private name often resolves only inside a VPN.",
			NextStep: fmt.Sprintf("Check the spelling of %q, and whether it resolves from this machine. An IP address in the host field bypasses DNS entirely.", c.Host),
		}

	case driver.FailureTimeout:
		return Diagnosis{
			Summary:  fmt.Sprintf("No answer from %s before the timeout.", address),
			Cause:    "Something is dropping the packets rather than refusing them, which is what a firewall or a security group usually does. The port may also be right but unreachable from this network.",
			NextStep: fmt.Sprintf("Check that this machine is allowed to reach port %d on %s, through the VPN if there is one.", c.Port, c.Host),
		}

	case driver.FailureRefused:
		return Diagnosis{
			Summary:  fmt.Sprintf("%s refused the connection.", address),
			Cause:    "The address answered, so the machine is reachable: nothing is listening on that port. The server may be stopped, on another port, or bound only to localhost.",
			NextStep: fmt.Sprintf("Check that PostgreSQL is running and listening on %d, and that listen_addresses covers the address this machine connects from.", c.Port),
		}

	case driver.FailureDropped:
		return Diagnosis{
			Summary:  fmt.Sprintf("The connection to %s was dropped.", address),
			Cause:    "It was established and then lost: the server was stopped or restarted, an administrator terminated the backend, or something between here and there closed the socket.",
			NextStep: "Try again — a pool opens a new connection on the next attempt. If it keeps happening, check whether the server is restarting or whether an idle timeout is closing connections.",
		}

	case driver.FailureTLS:
		return Diagnosis{
			Summary:  fmt.Sprintf("The TLS handshake with %s failed.", address),
			Cause:    "Either the server does not offer TLS, or its certificate was not accepted: an unknown authority, or a name that does not match the host being connected to.",
			NextStep: "Check the sslmode. With verify-ca and verify-full the root certificate must be the one that signed the server, and verify-full also requires the host here to match the certificate.",
		}

	case driver.FailureAuth:
		return Diagnosis{
			Summary:  fmt.Sprintf("The server rejected the credentials for %q.", c.User),
			Cause:    "The password is wrong, or the role does not exist. The server answered, so everything up to authentication is working.",
			NextStep: fmt.Sprintf("Check the password for %q, and that the role exists on this server.", c.User),
		}

	case driver.FailureNotAuthorized:
		return Diagnosis{
			Summary:  fmt.Sprintf("The server has no rule allowing %q to connect from this address.", c.User),
			Cause:    "pg_hba.conf decides who may connect from where, and it is consulted before the password is. No rule matched this user, this database and this client address.",
			NextStep: fmt.Sprintf("Add a pg_hba.conf line covering %q from this address and reload the server. This is a change on the server, not in these settings.", c.User),
		}

	case driver.FailureReadOnly:
		// Two problems wearing one SQLSTATE, fixed in two different places. The
		// mark is a checkbox in this window; a server that refuses writes on
		// its own is somebody else's server, and nothing here will change it.
		if c.ReadOnly {
			return Diagnosis{
				Summary:  "This connection is marked read-only, so the server refused a statement that writes.",
				Cause:    "Hermes opens a connection marked read-only with default_transaction_read_only on, and the server refuses every insert, update, delete and DDL inside it. The refusal comes from the server, so nothing in this window went around it.",
				NextStep: fmt.Sprintf("Clear the read-only mark on this connection and open it again, if writing to %s is what you meant to do.", address),
			}
		}

		return Diagnosis{
			Summary:  fmt.Sprintf("%s refused a statement that writes.", address),
			Cause:    "This connection is not marked read-only here, so the refusal is the server's own: a standby accepts no writes, and a server, a database or a role can be left with default_transaction_read_only on.",
			NextStep: fmt.Sprintf("Check whether %s is a replica, and whether default_transaction_read_only is set on the server, on the database or on the role %q.", c.Host, c.User),
		}

	case driver.FailureMissingDatabase:
		// Blaming a name the user never typed is how a message stops being
		// useful. When no database was named, what is missing is the default.
		if strings.TrimSpace(c.Database) == "" {
			return Diagnosis{
				Summary: fmt.Sprintf("No database was given, and %s has no %q database to connect to instead.",
					address, MaintenanceDatabase),
				Cause:    "Connecting without naming a database opens the maintenance database, which almost every server has. This one does not, or this role may not connect to it.",
				NextStep: "Name a database you can connect to in the form.",
			}
		}

		return Diagnosis{
			Summary:  fmt.Sprintf("The database %q does not exist on %s.", c.Database, address),
			Cause:    "The server accepted the credentials and then found no database by that name. Database names are case-sensitive when they were created quoted.",
			NextStep: fmt.Sprintf("Check the spelling of %q, or connect to postgres and list the databases.", c.Database),
		}

	default:
		return Diagnosis{
			Summary:  fmt.Sprintf("The connection to %s failed for a reason Hermes does not recognise.", address),
			Cause:    "This is not one of the failures Hermes knows how to explain, so the driver's own message below is the best available description.",
			NextStep: "Read the detail below. If it turns out to be a common failure, it is worth reporting so that it gets a proper explanation.",
		}
	}
}
