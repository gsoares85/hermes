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
