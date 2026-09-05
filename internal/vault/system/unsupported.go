//go:build !darwin && !linux && !windows

package system

import (
	"context"
	"fmt"
	"runtime"

	"github.com/gsoares85/hermes/internal/core/secret"
)

// Backend names the store to a person, and Advice says what to do when it does
// not answer. Both are prose: they end up in the warning the window shows, and
// somebody has to be able to act on them.
const (
	Backend = "a keychain of this operating system"
	Advice  = "Hermes stores passwords in the keychain of Linux, macOS and Windows only."
)

// Open exists so the package compiles wherever Go does. A platform with
// no implementation is not a build failure and not a panic: it is a machine
// without a keychain, which is a case this package already answers with the
// in-memory vault and a warning.
func Open(context.Context) (Vault, error) {
	return nil, fmt.Errorf("%w: Hermes has no keychain implementation for %s", secret.ErrUnavailable, runtime.GOOS)
}
