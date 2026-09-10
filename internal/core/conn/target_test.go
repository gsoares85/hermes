package conn_test

import (
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
)

func TestTargetCarriesWhatTheEngineNeeds(t *testing.T) {
	t.Parallel()

	config := sample()
	config.TLS = conn.TLS{Mode: conn.SSLVerifyFull, RootCert: "/ca.pem", Cert: "/c.pem", Key: "/k.pem"}

	got := config.Target()

	if got.Host != "db.example.com" || got.Port != 5432 {
		t.Errorf("address = %s:%d, want db.example.com:5432", got.Host, got.Port)
	}
	if got.Database != "hermes" || got.User != "hermes" {
		t.Errorf("database/user = %s/%s, want hermes/hermes", got.Database, got.User)
	}
	if got.Password != "s3cr3t" {
		t.Errorf("Password = %q, want the secret to reach the driver", got.Password)
	}
	if got.SSLMode != "verify-full" {
		t.Errorf("SSLMode = %q, want verify-full", got.SSLMode)
	}
	if got.RootCert != "/ca.pem" || got.Cert != "/c.pem" || got.Key != "/k.pem" {
		t.Errorf("certificate paths = %q/%q/%q, want them carried", got.RootCert, got.Cert, got.Key)
	}
	if got.Params["application_name"] != "hermes" {
		t.Errorf("Params = %v, want the session parameters carried", got.Params)
	}
}

// Name and Archived describe a saved connection, not a server. Letting them
// cross into the engine layer would be the first leak of a product concept into
// the driver seam.
func TestTargetDropsWhatIsNotTheEngineBusiness(t *testing.T) {
	t.Parallel()

	config := sample()
	config.Name = "staging"
	config.Archived = true

	rendered := strings.ToLower(strings.Join([]string{
		config.Target().Host,
		config.Target().Database,
		config.Target().User,
	}, " "))

	if strings.Contains(rendered, "staging") {
		t.Error("the connection name reached the engine target")
	}
}

// The parameter map has the same aliasing problem the config had: handing the
// driver the very map the user edits in the form would let a later edit change
// a live connection under it.
func TestTargetDoesNotShareTheParameterMap(t *testing.T) {
	t.Parallel()

	config := sample()
	target := config.Target()

	config.Params["application_name"] = "changed after the target was built"

	if target.Params["application_name"] != "hermes" {
		t.Errorf("target param = %q, want the value captured when the target was built",
			target.Params["application_name"])
	}
}

func TestTargetOfAConfigWithoutParams(t *testing.T) {
	t.Parallel()

	config := conn.Config{Host: "localhost", Port: 5432, Database: "hermes", User: "hermes"}
	if got := config.Target().Params; got != nil {
		t.Errorf("Params = %v, want nil", got)
	}
}

// The whole of the read-only mark, as far as this layer is concerned: it
// reaches the server as a session parameter.
//
// Deciding in the client whether a statement writes would need a SQL parser and
// would be wrong about the first function that writes inside itself. Saying it
// once, on connect, makes the server the thing that refuses — and nothing the
// window forgets to check can go around it.
func TestAReadOnlyConnectionTellsTheServerToRefuseWrites(t *testing.T) {
	t.Parallel()

	config := sample()
	config.ReadOnly = true

	if got := config.Target().Params["default_transaction_read_only"]; got != "on" {
		t.Errorf("default_transaction_read_only = %q, want on", got)
	}
}

// A connection nobody marked is left exactly as it was. Sending the parameter
// as off would override a server, a database or a role that was deliberately
// set read-only, which is the opposite of what not marking anything means.
func TestAnUnmarkedConnectionSaysNothingAboutReadingOnly(t *testing.T) {
	t.Parallel()

	if _, set := sample().Target().Params["default_transaction_read_only"]; set {
		t.Error("an unmarked connection sends default_transaction_read_only, and it should say nothing")
	}
}

// The mark wins over the parameter. Params is free-form text, so a connection
// marked read-only in the form could otherwise be un-marked by a line further
// down the same form — a protection turned off by something that never
// mentioned it.
func TestTheReadOnlyMarkOverridesTheSessionParameter(t *testing.T) {
	t.Parallel()

	config := sample()
	config.ReadOnly = true
	config.Params = map[string]string{"default_transaction_read_only": "off"}

	if got := config.Target().Params["default_transaction_read_only"]; got != "on" {
		t.Errorf("default_transaction_read_only = %q, want the mark to win", got)
	}
}

// The parameter map is the caller's, and a connection that had none must not
// come back having grown one — which is the shape a nil map makes easy to get
// wrong in both directions.
func TestMarkingReadOnlyDoesNotReachTheConfiguration(t *testing.T) {
	t.Parallel()

	config := sample()
	config.ReadOnly = true
	config.Params = nil

	if got := config.Target().Params["default_transaction_read_only"]; got != "on" {
		t.Errorf("default_transaction_read_only = %q, want on even with no parameters", got)
	}
	if config.Params != nil {
		t.Errorf("Params = %v, want the configuration left alone", config.Params)
	}
}
