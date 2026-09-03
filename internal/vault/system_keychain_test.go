//go:build keychain

// The suite behind the keychain tag talks to the real store of the machine it
// runs on, so it is not part of the ordinary test run: on a headless runner
// with no keyring it would fail for a reason that says nothing about the code.
// It is a job of its own, on all three systems, and there it is expected to
// pass — not to skip.
//
//	go test -tags=keychain ./internal/vault/...
package vault_test

import (
	"testing"

	"github.com/gsoares85/hermes/internal/core/secret"
	"github.com/gsoares85/hermes/internal/core/secret/secrettest"
	"github.com/gsoares85/hermes/internal/vault"
)

// The store of the operating system answers the same contract the in-memory
// vault does, checked by the same suite.
//
// It fails rather than skips when the machine has no keyring. A skip here would
// turn the one job that proves this code against a real keychain into a job
// that passes by doing nothing, and the difference would only show up on the
// machine of someone trying to save a password.
func TestTheStoreOfTheSystemHonoursTheContract(t *testing.T) {
	opened, status := vault.Open(t.Context())
	t.Cleanup(func() {
		if err := opened.Close(); err != nil {
			t.Errorf("closing the vault: %v", err)
		}
	})

	if status.Warning != "" {
		t.Fatalf("this machine has no usable keychain, so nothing was tested: %s", status.Warning)
	}
	if status.Backend == vault.BackendMemory {
		t.Fatalf("the vault fell back to memory without a warning")
	}

	t.Logf("running the contract against %s", status.Backend)

	secrettest.Run(t, func(t *testing.T) secret.Vault {
		t.Helper()

		return opened
	})
}
