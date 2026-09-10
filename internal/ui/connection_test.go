package ui_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/core/secret"
	"github.com/gsoares85/hermes/internal/driver"
	"github.com/gsoares85/hermes/internal/ui"
)

const password = "s3cr3t"

func form() ui.ConnectionForm {
	return ui.ConnectionForm{
		Name: "staging", Host: "db.example.com", Port: 5432,
		Database: "hermes", User: "hermes", Password: password,
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
		ui.SavedView{},
		ui.VaultView{},
		ui.NodeView{},
		ui.PropertiesView{},
		ui.ColumnView{},
	}

	// The walk itself is checked before it is trusted. A password is planted in
	// the nested field and the helper must find it — the previous version
	// returned "<ui.DiagnosisView Value>" there and would have reported a real
	// leak as clean.
	planted := ui.StatusView{
		ID:        "1",
		State:     "down",
		Diagnosis: ui.DiagnosisView{Failed: true, Detail: "postgres://hermes:" + password + "@host/db"},
	}
	if !strings.Contains(renderAll(planted), password) {
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
					// password; anything that would hold the value itself does.
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

	service := service(stubOpener{})

	got, err := service.Parse("postgres://hermes:" + password + "@db.example.com:5433/app?sslmode=require")
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

	if rendered := renderAll(got); strings.Contains(rendered, password) {
		t.Errorf("the parsed form carried the password: %s", rendered)
	}
}

func TestParseReportsAMalformedURI(t *testing.T) {
	t.Parallel()

	service := service(stubOpener{})

	if _, err := service.Parse("mysql://nope/app"); err == nil {
		t.Error("Parse accepted a URI that is not a postgres one")
	}
}

// A failed test is the expected outcome of testing a connection, so it comes
// back as something to render rather than as an error to handle.
func TestTestReturnsADiagnosisRatherThanAnError(t *testing.T) {
	t.Parallel()

	service := service(stubOpener{
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

	service := service(stubOpener{})

	if got := service.Test(t.Context(), form()); got.Failed {
		t.Errorf("a working connection reported a failure: %+v", got)
	}
}

// Whatever the driver says, the answer that reaches the window must not carry
// the password the window just sent.
func TestADiagnosisNeverCarriesThePassword(t *testing.T) {
	t.Parallel()

	service := service(stubOpener{
		pingErr: &driver.Failure{
			Class: driver.FailureAuth,
			Err:   errors.New("failed: postgres://hermes:" + password + "@db.example.com/hermes"),
		},
	})

	got := service.Test(t.Context(), form())
	if rendered := renderAll(got); strings.Contains(rendered, password) {
		t.Errorf("the diagnosis leaked the password: %s", rendered)
	}
}

func TestOpenKeepsTheConnectionAndReportsIt(t *testing.T) {
	t.Parallel()

	service := service(stubOpener{})

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

	service := service(stubOpener{})

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

	service := service(stubOpener{})

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

	service := service(stubOpener{})

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

	got := service(stubOpener{}).SSLModes()
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
// never descended into it — so the field most likely to carry a password, the
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

// service builds the boundary the way the application wires it, with the
// in-memory vault standing in for the keychain and an empty store.
//
// The vault is the real one rather than a stub on purpose: it is the same type
// that runs on a machine without a keyring, so a test that passes here is a
// test that passes there.
func service(opener driver.Opener) *ui.ConnectionService {
	return ui.NewConnectionService(ui.Dependencies{
		Opener:      opener,
		Store:       &memoryStore{},
		Vault:       secret.NewMemory(),
		VaultStatus: reporting(ui.VaultView{Backend: "keychain"}),
	})
}

// memoryStore is the connections file, without the file.
type memoryStore struct {
	saved   []conn.Config
	loadErr error
	saveErr error
}

func (m *memoryStore) Load() ([]conn.Config, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}

	return append([]conn.Config(nil), m.saved...), nil
}

func (m *memoryStore) Save(connections []conn.Config) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.saved = append([]conn.Config(nil), connections...)

	return nil
}

// The one type crossing this boundary that carries a password is the one that
// must not print itself. %v reaches a log the moment anyone logs the call the
// form arrived on, and the redaction is a net, not a proof.
func TestTheFormNeverPrintsThePassword(t *testing.T) {
	t.Parallel()

	form := ui.ConnectionForm{
		Name: "production", Host: "db.example.com", Port: 5432,
		Database: "app", User: "reporting", Password: "s3cr3t",
	}

	var logged bytes.Buffer
	slog.New(slog.NewTextHandler(&logged, nil)).Info("saving", "form", form)

	printed := map[string]string{
		"String": form.String(),
		"%v":     fmt.Sprintf("%v", form),
		"%+v":    fmt.Sprintf("%+v", form),
		"slog":   logged.String(),
	}

	for how, text := range printed {
		if strings.Contains(text, "s3cr3t") {
			t.Errorf("the form printed by %s is %q, the secret survived", how, text)
		}
		// Useless is not the same as safe: what is left has to still identify
		// the connection someone is reading the log about.
		if !strings.Contains(text, "db.example.com") {
			t.Errorf("the form printed by %s is %q, want it to still name the host", how, text)
		}
	}
}

// The environments belong in one place for the same reason the modes do: a
// window that retypes the list drifts from what the file will accept, and the
// value that drifts is the one marking a production server.
func TestEnvironmentsComeFromTheCore(t *testing.T) {
	t.Parallel()

	got := service(stubOpener{}).Environments()
	if len(got) != 3 {
		t.Fatalf("Environments() has %d entries, want three: %v", len(got), got)
	}
	for _, want := range []string{"dev", "staging", "prod"} {
		if !slicesContains(got, want) {
			t.Errorf("Environments() = %v, missing %q", got, want)
		}
	}
}

// The label is a fact about the connection, and every tab drawing that
// connection is handed it.
//
// "Unmistakable in any tab" is not something a tab can arrange for itself: it
// can only draw what it was given, so the state it draws has to carry the
// environment and the read-only mark and not just a state word.
func TestTheStateOfAConnectionCarriesItsLabel(t *testing.T) {
	t.Parallel()

	service := service(stubOpener{})

	marked := form()
	marked.Name = "billing — production"
	marked.Environment = "prod"
	marked.ReadOnly = true

	opened, err := service.Open(t.Context(), marked)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	for _, status := range []ui.StatusView{opened, statusOf(t, service, opened.ID)} {
		if status.Name != "billing — production" {
			t.Errorf("Name = %q, want the name of the connection", status.Name)
		}
		if status.Environment != "prod" {
			t.Errorf("Environment = %q, want prod", status.Environment)
		}
		if !status.ReadOnly {
			t.Error("the state of a connection marked read-only does not say so")
		}
	}
}

func statusOf(t *testing.T, service *ui.ConnectionService, id string) ui.StatusView {
	t.Helper()

	status, err := service.Status(id)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}

	return status
}

// The version the server reports, for the corner of the window that says which
// server this is.
//
// A call of its own rather than a field on the state: reading the state must
// not reach the network — a window repaints far more often than a server
// changes version, and a status read that dialled would turn every repaint into
// a round trip and every unreachable server into a freeze.
func TestTheServerSaysWhichVersionItIs(t *testing.T) {
	t.Parallel()

	service := service(stubOpener{})

	opened, err := service.Open(t.Context(), form())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	version, err := service.ServerVersion(t.Context(), opened.ID)
	if err != nil {
		t.Fatalf("ServerVersion returned error: %v", err)
	}

	if version != "16.2" {
		t.Errorf("ServerVersion() = %q, want what the server answered", version)
	}
}

func TestAConnectionThatIsNotOpenHasNoVersion(t *testing.T) {
	t.Parallel()

	if _, err := service(stubOpener{}).ServerVersion(t.Context(), "nope"); err == nil {
		t.Error("a connection that was never opened answered a version")
	}
}

// The status carries where the connection goes, so that a window can say which
// database and which role it is looking at without asking a second question.
// None of it is a secret: it is what the form was filled in with.
func TestTheStateOfAConnectionSaysWhereItGoes(t *testing.T) {
	t.Parallel()

	service := service(stubOpener{})

	opened, err := service.Open(t.Context(), form())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	if opened.Host != "db.example.com" || opened.Port != 5432 {
		t.Errorf("address = %s:%d, want db.example.com:5432", opened.Host, opened.Port)
	}
	if opened.Database != "hermes" || opened.User != "hermes" {
		t.Errorf("database/user = %s/%s, want hermes/hermes", opened.Database, opened.User)
	}
}

// The state of an open connection says which saved connection it came from.
//
// Without it the window cannot tell that the row somebody just clicked is
// already open: the identifier a connection is addressed by is minted when it
// opens and has nothing to do with the one the file keeps. Two identifiers that
// are never equal, compared, is a highlight that never shows and a second tab
// onto a server that already has one.
func TestTheStateSaysWhichSavedConnectionItCameFrom(t *testing.T) {
	t.Parallel()

	service, _, _ := saved(t)

	stored, err := service.Save(t.Context(), form())
	if err != nil {
		t.Fatalf("Save() = %v", err)
	}

	from := form()
	from.ID = stored.ID

	opened, err := service.Open(t.Context(), from)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}

	if opened.SavedID != stored.ID {
		t.Errorf("SavedID = %q, want the identifier of the saved connection %q", opened.SavedID, stored.ID)
	}
	if opened.ID == opened.SavedID {
		t.Error("the handle and the saved identifier are the same value, so neither says anything the other does not")
	}
}

// One opened from a form that was never saved has none, which is the honest
// answer rather than a made-up one.
func TestAConnectionThatWasNeverSavedHasNoSavedIdentifier(t *testing.T) {
	t.Parallel()

	opened, err := service(stubOpener{}).Open(t.Context(), form())
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}

	if opened.SavedID != "" {
		t.Errorf("SavedID = %q, want empty for a connection that is not in the file", opened.SavedID)
	}
}
