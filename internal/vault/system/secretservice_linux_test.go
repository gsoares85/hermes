//go:build linux

package system

import (
	"errors"
	"testing"

	"github.com/gsoares85/hermes/internal/core/secret"
)

// The session is opened with the "plain" algorithm, and the reason that is
// acceptable is the socket: a path in the runtime directory of this user, which
// nobody else can open. On a bus reached over TCP — a container, a forwarded
// desktop — the argument is gone and the password would cross a network in
// clear text.
func TestABusThatLeavesTheMachineIsRefused(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"tcp":                     "tcp:host=192.168.1.10,port=12345",
		"a listed remote":         "unix:path=/run/user/1000/bus;tcp:host=10.0.0.2,port=1",
		"nonce over tcp":          "nonce-tcp:host=127.0.0.1,port=12345",
		"something nobody writes": "carrier-pigeon:coop=1",
	}

	for name, address := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := localBus(address)
			if err == nil {
				t.Fatalf("localBus(%q) = nil, want a refusal", address)
			}
			if !errors.Is(err, secret.ErrUnavailable) {
				t.Errorf("localBus(%q) = %v, want it to be unavailable so the fallback takes over", address, err)
			}
		})
	}
}

// A local bus is the ordinary case and must not be refused, including the one
// that is not set at all: the library then looks for the socket of the session,
// which is a path.
func TestABusThatStaysOnTheMachineIsAccepted(t *testing.T) {
	t.Parallel()

	for _, address := range []string{
		"",
		"   ",
		"unix:path=/run/user/1000/bus",
		"unix:abstract=/tmp/dbus-abc",
		"unixexec:path=/usr/bin/dbus-daemon",
		"unix:path=/run/user/1000/bus;unix:abstract=/tmp/dbus-abc",
	} {
		if err := localBus(address); err != nil {
			t.Errorf("localBus(%q) = %v, want nil", address, err)
		}
	}
}
