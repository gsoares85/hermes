//go:build !darwin && !linux && !windows

package vault

import (
	"context"
	"fmt"
	"runtime"

	"github.com/gsoares85/hermes/internal/core/secret"
)

const (
	systemBackend = "a keychain of this operating system"
	systemAdvice  = "Hermes stores passwords in the keychain of Linux, macOS and Windows only."
)

// openSystem exists so the package compiles wherever Go does. A platform with
// no implementation is not a build failure and not a panic: it is a machine
// without a keychain, which is a case this package already answers with the
// in-memory vault and a warning.
func openSystem(context.Context) (Vault, error) {
	return nil, fmt.Errorf("%w: Hermes has no keychain implementation for %s", secret.ErrUnavailable, runtime.GOOS)
}
