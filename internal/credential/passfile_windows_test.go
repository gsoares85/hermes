//go:build windows

package credential_test

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/gsoares85/hermes/internal/credential"
)

// Windows has no mode to set, so the guarantee is where the file is rather than
// what was done to it: it is created under the cache directory of this user,
// and inherits that directory's access list. This asserts what that inheritance
// has to mean — that no principal wider than this user can read it.
//
// The temporary directory of a test is itself under the cache directory of the
// user on Windows, so it inherits from the same place the real one does.
func TestThePasswordFileIsReadableOnlyByThisUser(t *testing.T) {
	t.Parallel()

	store := credential.NewStore(t.TempDir() + `\credentials`)

	handoff, err := store.InFile(target())
	if err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}
	defer func() { _ = handoff.Release() }()

	path := passfileOf(t, handoff)
	access := accessListOf(t, path)

	// Both spellings of each principal, because an access list can name one by
	// its abbreviation or by its identifier, and only one of the two forms
	// appearing is still an access list that lets the machine read the file.
	broad := map[string]string{
		"Everyone":            "WD",
		"everyone by id":      "S-1-1-0",
		"Authenticated Users": "AU",
		"authenticated by id": "S-1-5-11",
		"Users":               "BU",
		"users by id":         "S-1-5-32-545",
	}

	for name, principal := range broad {
		if strings.Contains(access, ";"+principal+")") {
			t.Errorf("the access list of %s is %q, which grants %s", path, access, name)
		}
	}
}

// accessListOf returns the security descriptor of a file in its identifier
// form, which names every principal by SID. The spelled-out form is translated
// into the language of the system, and a test that read it would pass or fail
// depending on where the machine was bought.
//
// Asked of Windows directly rather than through PowerShell. A subprocess brings
// its own ways to fail that have nothing to do with the file — a shell that is
// not on the path, a profile that writes to stderr, a policy that will not run
// the command — and every one of them reads here as an access list that could
// not be checked. This is one call, and what it answers is the descriptor
// itself.
func accessListOf(t *testing.T, path string) string {
	t.Helper()

	descriptor, err := windows.GetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("reading the access list of %s: %v", path, err)
	}

	access := descriptor.String()
	if !strings.Contains(access, "D:") {
		t.Fatalf("the access list read for %s is %q, which is not one", path, access)
	}

	return access
}
