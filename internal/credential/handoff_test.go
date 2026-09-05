package credential_test

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/credential"
)

const password = "s3cr3t"

// environmentOf returns what a child process would be started with once the
// handoff has been applied to it.
func environmentOf(t *testing.T, handoff credential.Handoff, inherited ...string) []string {
	t.Helper()

	command := exec.Command("hermes-test-does-not-run")
	command.Env = inherited
	handoff.Apply(command)

	return command.Env
}

func TestInEnvironmentCarriesThePasswordInPGPASSWORD(t *testing.T) {
	t.Parallel()

	handoff, err := credential.InEnvironment(password)
	if err != nil {
		t.Fatalf("InEnvironment(...) = %v", err)
	}

	got := environmentOf(t, handoff)
	if !slices.Contains(got, "PGPASSWORD="+password) {
		t.Errorf("environment = %v, want it to carry the password", got)
	}
}

// An empty password is a connection that authenticates some other way — by
// certificate, by peer, by a .pgpass the person already has. Handing the child
// an empty PGPASSWORD would turn that into a failed login.
func TestInEnvironmentWithNoPasswordChangesNothing(t *testing.T) {
	t.Parallel()

	handoff, err := credential.InEnvironment("")
	if err != nil {
		t.Fatalf("InEnvironment(\"\") = %v", err)
	}

	inherited := []string{"PATH=/usr/bin", "PGPASSWORD=theirs"}
	got := environmentOf(t, handoff, inherited...)
	if !slices.Equal(got, inherited) {
		t.Errorf("environment = %v, want the environment it was given: %v", got, inherited)
	}
}

// libpq reads PGPASSWORD in preference to PGPASSFILE, so a variable inherited
// from the session would win over the credential this package prepared — and
// the child would authenticate with the wrong password, or with a stale one.
func TestApplyReplacesAnInheritedCredential(t *testing.T) {
	t.Parallel()

	handoff, err := credential.InEnvironment(password)
	if err != nil {
		t.Fatalf("InEnvironment(...) = %v", err)
	}

	got := environmentOf(t, handoff,
		"PATH=/usr/bin", "PGPASSWORD=theirs", "PGPASSFILE=/home/someone/.pgpass", "PGHOST=localhost")

	for _, unwanted := range []string{"PGPASSWORD=theirs", "PGPASSFILE=/home/someone/.pgpass"} {
		if slices.Contains(got, unwanted) {
			t.Errorf("environment = %v, want %q gone", got, unwanted)
		}
	}
	for _, kept := range []string{"PATH=/usr/bin", "PGHOST=localhost", "PGPASSWORD=" + password} {
		if !slices.Contains(got, kept) {
			t.Errorf("environment = %v, want it to carry %q", got, kept)
		}
	}
}

// A password that cannot be written to a password file cannot be handed over by
// either route, and the two routes have to stay interchangeable: a caller that
// switches from the environment to a file must not discover then that the
// password it has been using all along cannot be represented.
func TestInEnvironmentRefusesAPasswordItCouldNotWriteToAFile(t *testing.T) {
	t.Parallel()

	for name, unrepresentable := range map[string]string{
		"a line break": "two\nlines",
		"a NUL":        "two\x00parts",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := credential.InEnvironment(unrepresentable); !errors.Is(err, credential.ErrUnrepresentable) {
				t.Errorf("InEnvironment(%q) = %v, want ErrUnrepresentable", unrepresentable, err)
			}
		})
	}
}

// The handoff is the one value in this package that holds a password, so it is
// the one value that must not print itself. %v reaches a log the moment anyone
// logs the operation it belongs to.
func TestHandoffNeverPrintsThePassword(t *testing.T) {
	t.Parallel()

	handoff, err := credential.InEnvironment(password)
	if err != nil {
		t.Fatalf("InEnvironment(...) = %v", err)
	}

	for verb, printed := range map[string]string{
		"String": handoff.String(),
		"%v":     fmt.Sprintf("%v", handoff),
		"%+v":    fmt.Sprintf("%+v", handoff),
	} {
		if strings.Contains(printed, password) {
			t.Errorf("the handoff printed by %s is %q, the secret survived", verb, printed)
		}
	}
}

// Nothing was written, so nothing has to be removed — and a caller that always
// defers Release must not have to ask which kind of handoff it got.
func TestReleaseOfAHandoffWithNoFileIsNotAFailure(t *testing.T) {
	t.Parallel()

	var nothing credential.Handoff
	if err := nothing.Release(); err != nil {
		t.Errorf("Release() = %v, want nil", err)
	}
}

// The other type in this package that holds a password. Handoff has printed
// itself safely since it was written; the target it is built from did not, and
// a target is what a caller assembles and is most likely to log while working
// out why a dump failed.
func TestTheTargetNeverPrintsThePassword(t *testing.T) {
	t.Parallel()

	printed := map[string]string{
		"String": target().String(),
		"%v":     fmt.Sprintf("%v", target()),
		"%+v":    fmt.Sprintf("%+v", target()),
	}

	for how, text := range printed {
		if strings.Contains(text, password) {
			t.Errorf("the target printed by %s is %q, the secret survived", how, text)
		}
		if !strings.Contains(text, "db.example.com") {
			t.Errorf("the target printed by %s is %q, want it to still name the host", how, text)
		}
	}
}

// The case-insensitive comparison exists because the environment of Windows is:
// PgPassword and PGPASSWORD are one variable there, and an exact match would
// leave the inherited one in place — where libpq would read it in preference to
// what this package prepared. The branch had no test at all, so the rule was
// written down and never exercised.
//
// It runs on every platform on purpose. On Unix the spelling is a different
// variable, one libpq does not read and nobody sets, and dropping it costs
// nothing; keeping one rule instead of two is the point, and a test that only
// ran on Windows would let the rule diverge everywhere else.
func TestApplyReplacesAnInheritedCredentialWhateverItsCase(t *testing.T) {
	t.Parallel()

	handoff, err := credential.InEnvironment(password)
	if err != nil {
		t.Fatalf("InEnvironment(...) = %v", err)
	}

	got := environmentOf(t, handoff,
		"Path=/usr/bin",
		"PgPassword=theirs",
		"pgpassfile=/home/someone/.pgpass",
		"PGPASSWORD=also-theirs",
		"PgHost=localhost")

	for _, unwanted := range []string{
		"PgPassword=theirs", "pgpassfile=/home/someone/.pgpass", "PGPASSWORD=also-theirs",
	} {
		if slices.Contains(got, unwanted) {
			t.Errorf("environment = %v, want %q gone", got, unwanted)
		}
	}
	for _, kept := range []string{"Path=/usr/bin", "PgHost=localhost", "PGPASSWORD=" + password} {
		if !slices.Contains(got, kept) {
			t.Errorf("environment = %v, want it to carry %q", got, kept)
		}
	}
}

// The rule is about the whole name, not about what a name starts with.
func TestApplyKeepsAVariableThatMerelyBeginsLikeOne(t *testing.T) {
	t.Parallel()

	handoff, err := credential.InEnvironment(password)
	if err != nil {
		t.Fatalf("InEnvironment(...) = %v", err)
	}

	got := environmentOf(t, handoff, "PGPASSWORD_FILE=/etc/secret", "PGPASSFILEDIR=/tmp")

	for _, kept := range []string{"PGPASSWORD_FILE=/etc/secret", "PGPASSFILEDIR=/tmp"} {
		if !slices.Contains(got, kept) {
			t.Errorf("environment = %v, want it to keep %q", got, kept)
		}
	}
}
