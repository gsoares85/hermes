package conn_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
)

// A connection keyword and a session parameter look identical in a URI and are
// completely different things. Sending the first as the second reaches a server
// that has no business with it — and for the ones that pin the authentication
// method, it removes the protection while leaving it looking set.
func TestConnectionKeywordsDoNotBecomeSessionParameters(t *testing.T) {
	t.Parallel()

	keywords := []string{
		"require_auth",
		"channel_binding",
		"connect_timeout",
		"target_session_attrs",
		"passfile",
		"service",
		"sslsni",
		"gssencmode",
	}

	for _, key := range keywords {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			got, err := conn.ParseURI("postgres://hermes@localhost/app?" + key + "=value")
			if err != nil {
				t.Fatalf("ParseURI returned error: %v", err)
			}

			if _, sent := got.Params[key]; sent {
				t.Errorf("%s was put among the session parameters, which sends it to the server", key)
			}
			if got.Options[key] != "value" {
				t.Errorf("Options[%q] = %q, want it carried as a connection setting", key, got.Options[key])
			}
		})
	}
}

// The passphrase of a client key cannot go through the connection string, which
// is built without secrets on purpose. Refusing is the honest answer; putting
// it among the session parameters would send it to the server.
func TestAnEncryptedClientKeyIsRefused(t *testing.T) {
	t.Parallel()

	_, err := conn.ParseURI("postgres://hermes@localhost/app?sslpassword=phrase")
	if !errors.Is(err, conn.ErrInvalidURI) {
		t.Fatalf("ParseURI error = %v, want ErrInvalidURI", err)
	}
	if err != nil && strings.Contains(err.Error(), "phrase") {
		t.Errorf("the error echoed the passphrase: %v", err)
	}
}

// Real session parameters must still get through, or the separation would have
// been bought by breaking what it was meant to protect.
func TestSessionParametersStillReachTheServer(t *testing.T) {
	t.Parallel()

	got, err := conn.ParseURI("postgres://hermes@localhost/app?application_name=hermes&search_path=public&statement_timeout=5s")
	if err != nil {
		t.Fatalf("ParseURI returned error: %v", err)
	}

	for key, want := range map[string]string{
		"application_name":  "hermes",
		"search_path":       "public",
		"statement_timeout": "5s",
	} {
		if got.Params[key] != want {
			t.Errorf("Params[%q] = %q, want %q", key, got.Params[key], want)
		}
		if _, misplaced := got.Options[key]; misplaced {
			t.Errorf("%q was treated as a connection setting", key)
		}
	}
}

// Both maps have the aliasing problem Clone exists for.
func TestCloneDoesNotShareTheOptionMap(t *testing.T) {
	t.Parallel()

	original := conn.Config{
		Host: "localhost", Port: 5432, User: "hermes",
		Options: map[string]string{"connect_timeout": "10"},
	}
	copied := original.Clone()
	copied.Options["connect_timeout"] = "30"

	if original.Options["connect_timeout"] != "10" {
		t.Errorf("editing the copy changed the original: %v", original.Options)
	}
}

// The separation is only real if it survives the trip to the engine.
func TestTargetKeepsTheTwoMapsApart(t *testing.T) {
	t.Parallel()

	config := conn.Config{
		Host: "localhost", Port: 5432, User: "hermes",
		Params:  map[string]string{"application_name": "hermes"},
		Options: map[string]string{"require_auth": "scram-sha-256"},
	}

	target := config.Target()

	if target.Params["application_name"] != "hermes" {
		t.Errorf("Params = %v, want the session parameter carried", target.Params)
	}
	if target.Options["require_auth"] != "scram-sha-256" {
		t.Errorf("Options = %v, want the connection setting carried", target.Options)
	}
	if _, leaked := target.Params["require_auth"]; leaked {
		t.Error("the connection setting reached the server-bound parameters")
	}
}
