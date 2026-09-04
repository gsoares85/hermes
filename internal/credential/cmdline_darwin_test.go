//go:build darwin

package credential_test

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// commandLineOf reads the command line of a running process the way anyone on
// this machine would. The wide flag is not optional: ps truncates to the width
// of the terminal by default, and a truncated command line is a test that
// passes because it stopped reading before the argument it was looking for.
func commandLineOf(t *testing.T, pid int) string {
	t.Helper()

	output, err := exec.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("reading the command line of process %d: %v", pid, err)
	}

	return strings.TrimSpace(string(output))
}
