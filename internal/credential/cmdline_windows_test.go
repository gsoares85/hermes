//go:build windows

package credential_test

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// commandLineOf reads the command line of a running process the way anyone on
// this machine would. Windows keeps it in the process table rather than in a
// file, and CIM is what reads that table — wmic, which used to, is gone from
// current Windows.
func commandLineOf(t *testing.T, pid int) string {
	t.Helper()

	query := fmt.Sprintf("(Get-CimInstance Win32_Process -Filter 'ProcessId = %d').CommandLine", pid)

	output, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", query).Output()
	if err != nil {
		t.Fatalf("reading the command line of process %d: %v", pid, err)
	}

	return strings.TrimSpace(string(output))
}
