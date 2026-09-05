//go:build linux

package credential_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// commandLineOf reads the command line of a running process the way any other
// process on this machine would: straight out of /proc, which is exactly the
// exposure this package exists to avoid.
func commandLineOf(t *testing.T, pid int) string {
	t.Helper()

	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		t.Fatalf("reading the command line of process %d: %v", pid, err)
	}

	// The arguments are separated by NUL, and a trailing one closes the last.
	return strings.ReplaceAll(strings.TrimSuffix(string(raw), "\x00"), "\x00", " ")
}
