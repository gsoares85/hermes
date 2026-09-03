package secret

import (
	"context"
	"fmt"
	"sync"
)

// Memory is a vault that lives in the process and dies with it.
//
// It has two jobs, and they are the same code on purpose. It is the double the
// core is tested against, without a keychain, a session bus or Docker. And it is
// the real vault of anyone whose system offers no keychain: ADR-0010 chose it
// over a file because, with no master password to derive a key from, an
// encrypted file would keep its key on disk beside itself — plaintext with extra
// steps, and worse for looking safe. The cost is that the password is asked for
// again every time the application starts. The gain is that nothing is ever
// written anywhere.
//
// One code path serving both is what keeps the fallback honest: a branch only
// users walk is a branch nobody has tested.
type Memory struct {
	mu     sync.RWMutex
	values map[Ref]string
}

// NewMemory creates an empty vault.
func NewMemory() *Memory {
	return &Memory{values: make(map[Ref]string)}
}

// Get returns the secret stored under the reference.
func (m *Memory) Get(ctx context.Context, ref Ref) (string, error) {
	if err := Usable(ctx, ref); err != nil {
		return "", err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	value, stored := m.values[ref]
	if !stored {
		return "", fmt.Errorf("%w: %v", ErrNotFound, ref)
	}

	return value, nil
}

// Set stores the secret, replacing whatever the reference held.
func (m *Memory) Set(ctx context.Context, ref Ref, value string) error {
	if err := Usable(ctx, ref); err != nil {
		return err
	}
	if value == "" {
		return fmt.Errorf("%w: %v", ErrEmptySecret, ref)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.values[ref] = value

	return nil
}

// Delete removes the secret, answering ErrNotFound when there was none.
func (m *Memory) Delete(ctx context.Context, ref Ref) error {
	if err := Usable(ctx, ref); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, stored := m.values[ref]; !stored {
		return fmt.Errorf("%w: %v", ErrNotFound, ref)
	}
	delete(m.values, ref)

	return nil
}
