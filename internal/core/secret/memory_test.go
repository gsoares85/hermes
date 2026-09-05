package secret_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/gsoares85/hermes/internal/core/secret"
	"github.com/gsoares85/hermes/internal/core/secret/secrettest"
)

// The in-memory vault answers the same contract as every keychain, which is the
// point of it: the core is tested against this one, and shipping to someone
// whose system has no keychain must not change what the core can rely on.
func TestMemoryObeysTheVaultContract(t *testing.T) {
	t.Parallel()

	secrettest.Run(t, func(*testing.T) secret.Vault {
		return secret.NewMemory()
	})
}

// Nothing is shared between two vaults, and nothing outlives one. This is the
// property the fallback is chosen for: without a keychain the secret lives in
// the session and dies with the process, so it is never written anywhere.
func TestMemoryKeepsNothingBetweenVaults(t *testing.T) {
	t.Parallel()

	ref := secret.ConnectionRef("some-id")

	stored := secret.NewMemory()
	if err := stored.Set(t.Context(), ref, "hunter2"); err != nil {
		t.Fatalf("storing the secret: %v", err)
	}

	if _, err := secret.NewMemory().Get(t.Context(), ref); !errors.Is(err, secret.ErrNotFound) {
		t.Errorf("a fresh vault read %v, want ErrNotFound: the secret outlived its vault", err)
	}
}

// The window reads a secret while a job writes another. Under -race, a vault
// that guarded nothing would fail here rather than on someone's machine.
func TestMemoryIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()

	vault := secret.NewMemory()

	var waiting sync.WaitGroup
	for index := range 32 {
		ref := secret.ConnectionRef(string(rune('a' + index%26)))

		waiting.Add(2)
		go func() {
			defer waiting.Done()

			if err := vault.Set(t.Context(), ref, "hunter2"); err != nil {
				t.Errorf("storing %v: %v", ref, err)
			}
		}()
		go func() {
			defer waiting.Done()

			if _, err := vault.Get(t.Context(), ref); err != nil && !errors.Is(err, secret.ErrNotFound) {
				t.Errorf("reading %v: %v", ref, err)
			}
		}()
	}

	waiting.Wait()
}
