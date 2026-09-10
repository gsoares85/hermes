package conn_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/driver"
)

func failure(class driver.FailureClass) error {
	return &driver.Failure{Class: class, Err: errors.New("the driver said something unreadable")}
}

// Every class the engine can report has to produce a diagnosis a person can act
// on. A class that falls through to a generic message is a class that was
// classified for nothing.
func TestEveryClassIsDiagnosed(t *testing.T) {
	t.Parallel()

	classes := []driver.FailureClass{
		driver.FailureDNS,
		driver.FailureTimeout,
		driver.FailureRefused,
		driver.FailureDropped,
		driver.FailureTLS,
		driver.FailureAuth,
		driver.FailureNotAuthorized,
		driver.FailureMissingDatabase,
		driver.FailureReadOnly,
	}

	seen := make(map[string]driver.FailureClass, len(classes))

	for _, class := range classes {
		t.Run(string(class), func(t *testing.T) {
			got := conn.Diagnose(failure(class), sample())

			if got.Class != class {
				t.Errorf("Class = %q, want %q", got.Class, class)
			}
			for name, text := range map[string]string{
				"Summary":  got.Summary,
				"Cause":    got.Cause,
				"NextStep": got.NextStep,
			} {
				if strings.TrimSpace(text) == "" {
					t.Errorf("%s is empty for class %q", name, class)
				}
			}
		})

		// Two classes sharing a summary would mean the user is shown the same
		// thing for problems with different fixes.
		got := conn.Diagnose(failure(class), sample())
		if previous, repeated := seen[got.Summary]; repeated {
			t.Errorf("class %q and %q produce the same summary %q", class, previous, got.Summary)
		}
		seen[got.Summary] = class
	}
}

// The diagnosis is what the user reads, so it has to name the thing that is
// wrong rather than describe the category of wrongness.
func TestDiagnosisNamesWhatFailed(t *testing.T) {
	t.Parallel()

	config := sample()

	cases := map[driver.FailureClass][]string{
		driver.FailureDNS:             {config.Host},
		driver.FailureTimeout:         {config.Host, "5432"},
		driver.FailureRefused:         {config.Host, "5432"},
		driver.FailureAuth:            {config.User},
		driver.FailureNotAuthorized:   {config.User},
		driver.FailureMissingDatabase: {config.Database},
	}

	for class, wanted := range cases {
		t.Run(string(class), func(t *testing.T) {
			t.Parallel()

			got := conn.Diagnose(failure(class), config)
			whole := got.Summary + " " + got.Cause + " " + got.NextStep

			for _, want := range wanted {
				if !strings.Contains(whole, want) {
					t.Errorf("the diagnosis for %q does not name %q: %+v", class, want, got)
				}
			}
		})
	}
}

// A wrong password and a missing pg_hba rule both come back as a rejected
// connection, and they are fixed in completely different places. Telling them
// apart is most of the value of translating the error at all.
func TestAuthAndAuthorizationAreDifferentDiagnoses(t *testing.T) {
	t.Parallel()

	auth := conn.Diagnose(failure(driver.FailureAuth), sample())
	hba := conn.Diagnose(failure(driver.FailureNotAuthorized), sample())

	if auth.NextStep == hba.NextStep {
		t.Errorf("a wrong password and a missing pg_hba rule suggest the same next step: %q", auth.NextStep)
	}
	if !strings.Contains(strings.ToLower(hba.Cause+hba.NextStep), "pg_hba") {
		t.Errorf("the authorization diagnosis never mentions pg_hba: %+v", hba)
	}
}

// Blaming a name the user never typed is how a message stops being useful.
func TestAMissingDefaultDatabaseIsExplainedAsSuch(t *testing.T) {
	t.Parallel()

	unnamed := sample()
	unnamed.Database = ""

	got := conn.Diagnose(failure(driver.FailureMissingDatabase), unnamed)

	if !strings.Contains(got.Summary, conn.MaintenanceDatabase) {
		t.Errorf("Summary = %q, want it to name the default that is missing", got.Summary)
	}
	if strings.Contains(got.Summary, `""`) {
		t.Errorf("Summary = %q, blames an empty name the user never typed", got.Summary)
	}

	named := sample()
	if same := conn.Diagnose(failure(driver.FailureMissingDatabase), named); same.Summary == got.Summary {
		t.Error("a named and an unnamed database produce the same message")
	}
}

// An error the engine could not classify must still produce something usable,
// and must not claim to know more than it does.
func TestUnclassifiedFailureIsHonest(t *testing.T) {
	t.Parallel()

	got := conn.Diagnose(errors.New("something nobody predicted"), sample())

	if got.Class != driver.FailureUnknown {
		t.Errorf("Class = %q, want %q", got.Class, driver.FailureUnknown)
	}
	if strings.TrimSpace(got.Summary) == "" || strings.TrimSpace(got.NextStep) == "" {
		t.Errorf("an unclassified failure produced an empty diagnosis: %+v", got)
	}
	if !strings.Contains(got.Detail, "something nobody predicted") {
		t.Errorf("Detail = %q, want it to carry the original error for whoever can read it", got.Detail)
	}
}

func TestDiagnoseOfNoErrorIsNotAFailure(t *testing.T) {
	t.Parallel()

	if got := conn.Diagnose(nil, sample()); got.Failed() {
		t.Errorf("Diagnose(nil) = %+v, want a diagnosis that did not fail", got)
	}
}

// The diagnosis is shown on screen, written to a log and pasted into bug
// reports. It is the single most likely place for the password to escape.
func TestDiagnosisNeverCarriesThePassword(t *testing.T) {
	t.Parallel()

	config := sample()
	leaky := &driver.Failure{
		Class: driver.FailureAuth,
		// A driver that echoed the connection string is exactly the case the
		// redaction exists for.
		Err: errors.New("failed to connect: postgres://hermes:s3cr3t@db.example.com:5432/hermes"),
	}

	got := conn.Diagnose(leaky, config)
	whole := got.Summary + got.Cause + got.NextStep + got.Detail + got.String()

	if strings.Contains(whole, "s3cr3t") {
		t.Errorf("the diagnosis leaked the password: %s", whole)
	}
}

func TestDiagnosisReadsAsOneLine(t *testing.T) {
	t.Parallel()

	got := conn.Diagnose(failure(driver.FailureRefused), sample())
	if !strings.Contains(got.String(), got.Summary) {
		t.Errorf("String() = %q, want it to carry the summary", got.String())
	}
}

// A refused write is two different problems wearing one SQLSTATE, and they are
// fixed in different places. Hermes marked the connection read-only, so the fix
// is a checkbox in this window; or it did not, and the server itself refuses
// writes — a standby, or a role left with the parameter on — and nothing in
// this window will change that.
func TestAMarkedConnectionIsToldItMarkedItself(t *testing.T) {
	t.Parallel()

	marked := sample()
	marked.ReadOnly = true

	mine := conn.Diagnose(failure(driver.FailureReadOnly), marked)
	theirs := conn.Diagnose(failure(driver.FailureReadOnly), sample())

	if !strings.Contains(strings.ToLower(mine.Summary+mine.Cause+mine.NextStep), "read-only") {
		t.Errorf("the diagnosis of a marked connection never says read-only: %+v", mine)
	}
	if mine.NextStep == theirs.NextStep {
		t.Errorf("a connection Hermes marked and a server that refuses writes suggest the same next step: %q",
			mine.NextStep)
	}
}
