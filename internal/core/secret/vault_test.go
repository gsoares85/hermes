package secret_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/secret"
)

// The account of a connection is its identifier and nothing else. Naming it
// after the connection would lose the password the first time someone renames
// one, and would make two connections called "prod" the same secret.
func TestConnectionRefAddressesTheIdentifier(t *testing.T) {
	t.Parallel()

	ref := secret.ConnectionRef("7b1f0c6e-1a4d-4f2f-9d6a-2a1c8e0b3f55")

	if ref.Service != secret.Service {
		t.Errorf("service = %q, want %q", ref.Service, secret.Service)
	}
	if ref.Account != "7b1f0c6e-1a4d-4f2f-9d6a-2a1c8e0b3f55" {
		t.Errorf("account = %q, want the connection identifier", ref.Account)
	}
}

func TestRefValidateAcceptsACompleteReference(t *testing.T) {
	t.Parallel()

	if err := secret.ConnectionRef("some-id").Validate(); err != nil {
		t.Errorf("a complete reference was refused: %v", err)
	}
}

func TestRefValidateRefusesAnUnusableReference(t *testing.T) {
	t.Parallel()

	cases := map[string]secret.Ref{
		"no service":           {Account: "some-id"},
		"no account":           {Service: secret.Service},
		"blank service":        {Service: "   ", Account: "some-id"},
		"blank account":        {Service: secret.Service, Account: "\t"},
		"nul byte in account":  {Service: secret.Service, Account: "some\x00id"},
		"newline in account":   {Service: secret.Service, Account: "some\nid"},
		"nul byte in service":  {Service: "Her\x00mes", Account: "some-id"},
		"control byte in acct": {Service: secret.Service, Account: "some\x01id"},
		"delete byte in acct":  {Service: secret.Service, Account: "some\x7fid"},
	}

	for name, ref := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := ref.Validate(); !errors.Is(err, secret.ErrInvalidRef) {
				t.Errorf("Validate returned %v, want ErrInvalidRef", err)
			}
		})
	}
}

// A NUL byte is refused rather than carried because the keychain APIs of macOS
// and Windows are C APIs: an account truncated at the NUL would make two
// different connections address one secret, and the second would silently
// overwrite the first.
func TestRefValidateExplainsWhyItRefused(t *testing.T) {
	t.Parallel()

	err := secret.Ref{Service: secret.Service, Account: "some\x00id"}.Validate()
	if err == nil {
		t.Fatal("an account with a NUL byte was accepted")
	}
	if !strings.Contains(err.Error(), "account") {
		t.Errorf("the error %q does not name the field that is wrong", err)
	}
}

// A reference is not a secret: it names one. Rendering it is how an error says
// which secret it failed on, so it has to read the same way twice.
func TestRefStringIsStableAndCarriesNoSecret(t *testing.T) {
	t.Parallel()

	ref := secret.ConnectionRef("some-id")

	if got, want := ref.String(), "Hermes/some-id"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	t.Parallel()

	distinct := []error{
		secret.ErrNotFound,
		secret.ErrUnavailable,
		secret.ErrInvalidRef,
		secret.ErrEmptySecret,
	}

	for i, first := range distinct {
		for j, second := range distinct {
			if i != j && errors.Is(first, second) {
				t.Errorf("%v and %v are the same error: a caller cannot tell them apart", first, second)
			}
		}
	}
}
