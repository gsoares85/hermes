package ui_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/driver"
	"github.com/gsoares85/hermes/internal/ui"
)

const secret = "s3cr3t"

func form() ui.ConnectionForm {
	return ui.ConnectionForm{
		Name: "staging", Host: "db.example.com", Port: 5432,
		Database: "hermes", User: "hermes", Password: secret,
		SSLMode: "disable",
	}
}

// stubPool answers however the test needs it to.
type stubPool struct{ pingErr error }

func (s stubPool) Ping(context.Context) error { return s.pingErr }
func (s stubPool) Session(context.Context) (driver.Session, error) {
	return nil, errors.New("not part of this test")
}
func (s stubPool) ServerVersion(context.Context) (string, error) { return "16.2", nil }
func (s stubPool) Databases(context.Context) ([]string, error) {
	if s.pingErr != nil {
		return nil, s.pingErr
	}

	return []string{"app", "hermes", "postgres"}, nil
}
func (s stubPool) Close() {}

type stubOpener struct {
	pingErr error
	openErr error
}

func (s stubOpener) Open(context.Context, driver.Target) (driver.Pool, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}

	return stubPool{pingErr: s.pingErr}, nil
}

// The rule of the boundary, checked by walking the types rather than by
// trusting that nobody adds a field later. Anything the service returns is
// inspected for a member that looks like a credential.
func TestNothingReturnedCarriesACredential(t *testing.T) {
	t.Parallel()

	returned := []any{
		ui.ConnectionView{},
		ui.DiagnosisView{},
		ui.StatusView{},
	}

	// The walk itself is checked before it is trusted. A secret is planted in
	// the nested field and the helper must find it — the previous version
	// returned "<ui.DiagnosisView Value>" there and would have reported a real
	// leak as clean.
	planted := ui.StatusView{
		ID:        "1",
		State:     "down",
		Diagnosis: ui.DiagnosisView{Failed: true, Detail: "postgres://hermes:" + secret + "@host/db"},
	}
	if !strings.Contains(renderAll(planted), secret) {
		t.Fatal("the field walk does not descend into nested structs, so it cannot detect a leak there")
	}

	forbidden := []string{"password", "secret", "credential", "token", "passphrase"}

	for _, value := range returned {
		typ := reflect.TypeOf(value)
		t.Run(typ.Name(), func(t *testing.T) {
			t.Parallel()

			for i := range typ.NumField() {
				name := strings.ToLower(typ.Field(i).Name)
				for _, banned := range forbidden {
					// HasPassword says whether one exists, and carries no
					// secret; anything that would hold the value itself does.
					if strings.Contains(name, banned) && !strings.HasPrefix(name, "has") {
						t.Errorf("%s.%s can carry a credential across the boundary",
							typ.Name(), typ.Field(i).Name)
					}
				}
			}
		})
	}
}

// Pasting a URI fills the form. The password is left behind on purpose: it
// would otherwise sit in the state of a page that is redrawn and inspected.
func TestParseFillsTheFormWithoutThePassword(t *testing.T) {
	t.Parallel()

	service := ui.NewConnectionService(stubOpener{})

	got, err := service.Parse("postgres://hermes:" + secret + "@db.example.com:5433/app?sslmode=require")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if got.Host != "db.example.com" || got.Port != 5433 || got.Database != "app" || got.User != "hermes" {
		t.Errorf("Parse = %+v, want the address and identity filled in", got)
	}
	if got.SSLMode != "require" {
		t.Errorf("SSLMode = %q, want require", got.SSLMode)
	}
	if !got.HasPassword {
		t.Error("HasPassword = false, want the form to know one was pasted")
	}

	if rendered := renderAll(got); strings.Contains(rendered, secret) {
		t.Errorf("the parsed form carried the password: %s", rendered)
	}
}

func TestParseReportsAMalformedURI(t *testing.T) {
	t.Parallel()

	service := ui.NewConnectionService(stubOpener{})

	if _, err := service.Parse("mysql://nope/app"); err == nil {
		t.Error("Parse accepted a URI that is not a postgres one")
	}
}

// A failed test is the expected outcome of testing a connection, so it comes
// back as something to render rather than as an error to handle.
func TestTestReturnsADiagnosisRatherThanAnError(t *testing.T) {
	t.Parallel()

	service := ui.NewConnectionService(stubOpener{
		pingErr: &driver.Failure{Class: driver.FailureAuth, Err: errors.New("rejected")},
	})

	got := service.Test(t.Context(), form())

	if !got.Failed {
		t.Fatal("a failing connection reported success")
	}
	if got.Class != string(driver.FailureAuth) {
		t.Errorf("Class = %q, want %q", got.Class, driver.FailureAuth)
	}
	if got.Summary == "" || got.NextStep == "" {
		t.Errorf("the diagnosis is not usable: %+v", got)
	}
}

func TestTestReportsSuccess(t *testing.T) {
	t.Parallel()

	service := ui.NewConnectionService(stubOpener{})

	if got := service.Test(t.Context(), form()); got.Failed {
		t.Errorf("a working connection reported a failure: %+v", got)
	}
}

// Whatever the driver says, the answer that reaches the window must not carry
// the password the window just sent.
func TestADiagnosisNeverCarriesThePassword(t *testing.T) {
	t.Parallel()

	service := ui.NewConnectionService(stubOpener{
		pingErr: &driver.Failure{
			Class: driver.FailureAuth,
			Err:   errors.New("failed: postgres://hermes:" + secret + "@db.example.com/hermes"),
		},
	})

	got := service.Test(t.Context(), form())
	if rendered := renderAll(got); strings.Contains(rendered, secret) {
		t.Errorf("the diagnosis leaked the password: %s", rendered)
	}
}

func TestOpenKeepsTheConnectionAndReportsIt(t *testing.T) {
	t.Parallel()

	service := ui.NewConnectionService(stubOpener{})

	opened, err := service.Open(t.Context(), form())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if opened.ID == "" {
		t.Fatal("Open returned no identifier")
	}
	if opened.State != "connected" {
		t.Errorf("State = %q, want connected", opened.State)
	}

	status, err := service.Status(opened.ID)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if status.State != "connected" {
		t.Errorf("Status = %q, want connected", status.State)
	}

	if err := service.Close(opened.ID); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if _, err := service.Status(opened.ID); err == nil {
		t.Error("a closed connection is still addressable")
	}
}

func TestOpenRejectsAFormThatCannotConnect(t *testing.T) {
	t.Parallel()

	service := ui.NewConnectionService(stubOpener{})

	broken := form()
	broken.Host = ""

	if _, err := service.Open(t.Context(), broken); err == nil {
		t.Error("Open accepted a form with no host")
	}
}

// Two connections opened from the window must not be handed the same
// identifier, or closing one would close the other.
func TestEveryOpenConnectionGetsItsOwnIdentifier(t *testing.T) {
	t.Parallel()

	service := ui.NewConnectionService(stubOpener{})

	first, err := service.Open(t.Context(), form())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	second, err := service.Open(t.Context(), form())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	if first.ID == second.ID {
		t.Errorf("both connections got the identifier %q", first.ID)
	}
}

func TestUnknownIdentifiersAreRejected(t *testing.T) {
	t.Parallel()

	service := ui.NewConnectionService(stubOpener{})

	if _, err := service.Status("nope"); err == nil {
		t.Error("Status accepted an identifier that was never opened")
	}
	if _, err := service.Check(t.Context(), "nope"); err == nil {
		t.Error("Check accepted an identifier that was never opened")
	}
	if err := service.Close("nope"); err == nil {
		t.Error("Close accepted an identifier that was never opened")
	}
}

// The list of modes belongs in one place. A window that retypes it drifts from
// what the driver accepts.
func TestSSLModesComeFromTheCore(t *testing.T) {
	t.Parallel()

	got := ui.NewConnectionService(stubOpener{}).SSLModes()
	if len(got) != 6 {
		t.Fatalf("SSLModes() has %d entries, want the six libpq modes: %v", len(got), got)
	}
	for _, want := range []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"} {
		if !slicesContains(got, want) {
			t.Errorf("SSLModes() = %v, missing %q", got, want)
		}
	}
}

// renderAll prints every field, however deeply nested.
//
// The earlier version called reflect.Value.String() field by field, which
// returns "<ui.DiagnosisView Value>" for anything that is not a string and
// never descended into it — so the field most likely to carry a secret, the
// driver detail inside a status, was never actually looked at.
func renderAll(value any) string {
	return strings.ToLower(fmt.Sprintf("%+v", value))
}

func slicesContains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}

	return false
}
