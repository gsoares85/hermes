package ui_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/core/secret"
	"github.com/gsoares85/hermes/internal/ui"
)

// saved builds the boundary over a store and a vault the test can look inside,
// which is the only way to assert that the password went to one of them and the
// connection to the other.
func saved(t *testing.T) (*ui.ConnectionService, *memoryStore, *secret.Memory) {
	t.Helper()

	store, vault := &memoryStore{}, secret.NewMemory()

	return ui.NewConnectionService(ui.Dependencies{
		Opener:      stubOpener{},
		Store:       store,
		Vault:       vault,
		VaultStatus: reporting(ui.VaultView{Backend: "keychain"}),
	}), store, vault
}

// The rule of this phase, checked on both sides at once: the connection is in
// the file, the password is in the keychain, and neither is in the other.
func TestSavingSplitsTheConnectionFromItsPassword(t *testing.T) {
	t.Parallel()

	service, store, vault := saved(t)

	view, err := service.Save(t.Context(), form())
	if err != nil {
		t.Fatalf("Save() = %v", err)
	}
	if view.ID == "" {
		t.Fatal("the saved connection has no identifier, so its password could never be found again")
	}

	if len(store.saved) != 1 {
		t.Fatalf("the store holds %d connections, want 1", len(store.saved))
	}
	if got := store.saved[0].Password; got != "" {
		t.Errorf("the stored connection carries the password %q", got)
	}

	stored, err := vault.Get(t.Context(), secret.ConnectionRef(view.ID))
	if err != nil {
		t.Fatalf("the password did not reach the vault: %v", err)
	}
	if stored != password {
		t.Errorf("the vault holds %q, want the password from the form", stored)
	}
}

// Saving twice with the same identifier is editing, not adding. Appending
// instead would leave the list growing a duplicate on every keystroke someone
// corrected.
func TestSavingAConnectionAgainReplacesIt(t *testing.T) {
	t.Parallel()

	service, store, _ := saved(t)

	first, err := service.Save(t.Context(), form())
	if err != nil {
		t.Fatalf("Save() = %v", err)
	}

	edited := form()
	edited.ID = first.ID
	edited.Host = "elsewhere.example.com"

	if _, err := service.Save(t.Context(), edited); err != nil {
		t.Fatalf("Save() = %v", err)
	}

	if len(store.saved) != 1 {
		t.Fatalf("the store holds %d connections, want the one that was edited", len(store.saved))
	}
	if store.saved[0].Host != "elsewhere.example.com" {
		t.Errorf("the stored host is %q, want the edited one", store.saved[0].Host)
	}
}

// Someone editing the host of a connection they saved last week leaves the
// password field empty. That is not a request to forget the password, and
// treating it as one would lose a secret to an edit that never mentioned it.
func TestEditingWithoutRetypingThePasswordKeepsIt(t *testing.T) {
	t.Parallel()

	service, _, vault := saved(t)

	first, err := service.Save(t.Context(), form())
	if err != nil {
		t.Fatalf("Save() = %v", err)
	}

	edited := form()
	edited.ID = first.ID
	edited.Password = ""

	if _, err = service.Save(t.Context(), edited); err != nil {
		t.Fatalf("Save() = %v", err)
	}

	stored, err := vault.Get(t.Context(), secret.ConnectionRef(first.ID))
	if err != nil {
		t.Fatalf("the password was lost to an edit that did not mention it: %v", err)
	}
	if stored != password {
		t.Errorf("the vault holds %q, want the password that was saved first", stored)
	}
}

func TestSavingRefusesAConnectionThatCannotConnect(t *testing.T) {
	t.Parallel()

	service, store, _ := saved(t)

	broken := form()
	broken.Host = ""

	if _, err := service.Save(t.Context(), broken); err == nil {
		t.Fatal("Save() accepted a connection with no host")
	}
	if len(store.saved) != 0 {
		t.Errorf("the store holds %d connections, want a refused save to write nothing", len(store.saved))
	}
}

// Listing is what the window draws on every redraw. Reading the keychain here
// would be an authorisation dialog per row on macOS and on Linux, for someone
// who only wanted to see what they had saved.
func TestListingNeverTouchesTheVault(t *testing.T) {
	t.Parallel()

	service, _, vault := saved(t)

	if _, err := service.Save(t.Context(), form()); err != nil {
		t.Fatalf("Save() = %v", err)
	}

	watched := &countingVault{Vault: vault}
	listing := ui.NewConnectionService(ui.Dependencies{
		Opener: stubOpener{},
		Store:  &memoryStore{saved: []conn.Config{{ID: "an-id", Host: "h", Port: 5432, User: "u"}}},
		Vault:  watched,
	})

	views, err := listing.List()
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("List() returned %d connections, want 1", len(views))
	}
	if watched.reads != 0 {
		t.Errorf("listing read the vault %d times, want none", watched.reads)
	}
}

// Deleting a connection has to take its password with it. A secret left behind
// is an item in the keychain of the person that nothing will ever look for
// again.
func TestDeletingTakesThePasswordWithIt(t *testing.T) {
	t.Parallel()

	service, store, vault := saved(t)

	view, err := service.Save(t.Context(), form())
	if err != nil {
		t.Fatalf("Save() = %v", err)
	}

	if err := service.Delete(t.Context(), view.ID); err != nil {
		t.Fatalf("Delete() = %v", err)
	}

	if len(store.saved) != 0 {
		t.Errorf("the store still holds %d connections", len(store.saved))
	}
	if _, err := vault.Get(t.Context(), secret.ConnectionRef(view.ID)); !errors.Is(err, secret.ErrNotFound) {
		t.Errorf("the password outlived the connection: Get() = %v, want ErrNotFound", err)
	}
}

// A connection saved without a password has no secret to remove, and removing
// it must still succeed: the outcome the person asked for is that the
// connection is gone, and it is.
func TestDeletingAConnectionThatNeverHadAPasswordWorks(t *testing.T) {
	t.Parallel()

	service, _, _ := saved(t)

	blank := form()
	blank.Password = ""

	view, err := service.Save(t.Context(), blank)
	if err != nil {
		t.Fatalf("Save() = %v", err)
	}

	if err := service.Delete(t.Context(), view.ID); err != nil {
		t.Errorf("Delete() = %v, want removing a connection with no password to succeed", err)
	}
}

func TestDeletingSomethingThatIsNotSavedIsReported(t *testing.T) {
	t.Parallel()

	service, _, _ := saved(t)

	if err := service.Delete(t.Context(), "never-saved"); err == nil {
		t.Error("Delete() = nil for a connection that is not saved")
	}
}

// The point of saving a password is not having to type it again. Opening a
// saved connection reads it from the vault, at the moment it is used.
func TestOpeningASavedConnectionUsesTheStoredPassword(t *testing.T) {
	t.Parallel()

	service, _, vault := saved(t)

	view, err := service.Save(t.Context(), form())
	if err != nil {
		t.Fatalf("Save() = %v", err)
	}

	watched := &countingVault{Vault: vault}
	opening := ui.NewConnectionService(ui.Dependencies{
		Opener: stubOpener{},
		Store:  &memoryStore{},
		Vault:  watched,
	})

	reopened := ui.ConnectionForm{
		ID: view.ID, Host: view.Host, Port: view.Port,
		Database: view.Database, User: view.User, SSLMode: view.SSLMode,
	}

	status, err := opening.Open(t.Context(), reopened)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	t.Cleanup(func() { _ = opening.Close(status.ID) })

	if watched.reads != 1 {
		t.Errorf("opening read the vault %d times, want exactly one", watched.reads)
	}
}

// A connection nobody saved a password for still opens: plenty of servers
// authenticate by certificate, by peer, or by a .pgpass the person already has.
func TestOpeningWorksWhenNoPasswordWasEverStored(t *testing.T) {
	t.Parallel()

	service, _, _ := saved(t)

	blank := form()
	blank.Password = ""

	status, err := service.Open(t.Context(), blank)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	t.Cleanup(func() { _ = service.Close(status.ID) })
}

// Without the warning, a machine with no keyring looks exactly like one that
// has a keyring, right up until the passwords are gone.
func TestTheVaultStatusCarriesTheWarning(t *testing.T) {
	t.Parallel()

	service := ui.NewConnectionService(ui.Dependencies{
		Opener: stubOpener{},
		Store:  &memoryStore{},
		Vault:  secret.NewMemory(),
		VaultStatus: reporting(ui.VaultView{
			Backend: "in-memory",
			Warning: "Hermes could not reach the Secret Service of this desktop session.",
		}),
	})

	status, err := service.VaultStatus(t.Context())
	if err != nil {
		t.Fatalf("VaultStatus() = %v", err)
	}
	if status.Backend != "in-memory" {
		t.Errorf("the backend is %q, want the one the application was given", status.Backend)
	}
	if !strings.Contains(status.Warning, "Secret Service") {
		t.Errorf("the warning did not reach the window: %q", status.Warning)
	}
}

// A file someone broke by hand has to be reported, not swallowed into an empty
// list: a list that silently loses every saved connection is worse than an
// error, because it looks like nothing was ever there.
func TestAStoreThatCannotBeReadIsReported(t *testing.T) {
	t.Parallel()

	broken := errors.New("line 4, host: invalid connection: no host")
	service := ui.NewConnectionService(ui.Dependencies{
		Opener: stubOpener{},
		Store:  &memoryStore{loadErr: broken},
		Vault:  secret.NewMemory(),
	})

	if _, err := service.List(); err == nil || !strings.Contains(err.Error(), "line 4") {
		t.Errorf("List() = %v, want the fault the file has", err)
	}
}

// A store that refuses the write must leave the keychain untouched: a password
// belonging to a connection that was never saved is an item nothing can reach.
func TestAFailedWriteStoresNoPassword(t *testing.T) {
	t.Parallel()

	vault := secret.NewMemory()
	watched := &countingVault{Vault: vault}
	service := ui.NewConnectionService(ui.Dependencies{
		Opener: stubOpener{},
		Store:  &memoryStore{saveErr: errors.New("the disk is full")},
		Vault:  watched,
	})

	if _, err := service.Save(t.Context(), form()); err == nil {
		t.Fatal("Save() = nil, want the failure of the store")
	}
	if watched.writes != 0 {
		t.Errorf("the vault was written %d times for a connection that was not saved", watched.writes)
	}
}

// countingVault is the vault with a tally, which is how a test asserts that
// something was not read rather than that it was read correctly.
type countingVault struct {
	secret.Vault
	reads  int
	writes int
}

func (c *countingVault) Get(ctx context.Context, ref secret.Ref) (string, error) {
	c.reads++

	return c.Vault.Get(ctx, ref)
}

func (c *countingVault) Set(ctx context.Context, ref secret.Ref, value string) error {
	c.writes++

	return c.Vault.Set(ctx, ref, value)
}

// A keychain that refuses the write leaves the connection saved and its
// password not. That is a state the person has to be told about in those words,
// because the connection is there and will not connect.
func TestAVaultThatRefusesToStoreIsReported(t *testing.T) {
	t.Parallel()

	service := ui.NewConnectionService(ui.Dependencies{
		Opener: stubOpener{},
		Store:  &memoryStore{},
		Vault:  refusingVault{err: errors.New("the keychain is locked")},
	})

	_, err := service.Save(t.Context(), form())
	if err == nil {
		t.Fatal("Save() = nil, want the keychain refusal to be reported")
	}
	if !strings.Contains(err.Error(), "was saved") || !strings.Contains(err.Error(), "password was not") {
		t.Errorf("the error does not say what did and did not happen: %v", err)
	}
	if strings.Contains(err.Error(), password) {
		t.Errorf("the error quoted the password: %v", err)
	}
}

// The connection is gone from the file either way, so the message has to say
// that the password is the part still there rather than imply nothing happened.
func TestAVaultThatRefusesToForgetIsReported(t *testing.T) {
	t.Parallel()

	service := ui.NewConnectionService(ui.Dependencies{
		Opener: stubOpener{},
		Store:  &memoryStore{saved: []conn.Config{{ID: "an-id", Host: "h", Port: 5432, User: "u"}}},
		Vault:  refusingVault{err: errors.New("the keychain is locked")},
	})

	err := service.Delete(t.Context(), "an-id")
	if err == nil {
		t.Fatal("Delete() = nil, want the keychain refusal to be reported")
	}
	if !strings.Contains(err.Error(), "still in the keychain") {
		t.Errorf("the error does not say what is left behind: %v", err)
	}
}

// A keychain that will not open is not a reason to guess: connecting with no
// password when one was stored fails with an authentication error that says
// nothing about the real problem.
func TestAVaultThatRefusesToAnswerStopsTheConnection(t *testing.T) {
	t.Parallel()

	service := ui.NewConnectionService(ui.Dependencies{
		Opener: stubOpener{},
		Store:  &memoryStore{},
		Vault:  refusingVault{err: errors.New("the keychain is locked")},
	})

	saved := form()
	saved.ID = "an-id"
	saved.Password = ""

	if _, err := service.Open(t.Context(), saved); err == nil {
		t.Error("Open() = nil, want the keychain refusal to be reported")
	}
}

// refusingVault is a keychain that is there and will not cooperate, which is a
// locked one — a different thing from a machine that has none.
type refusingVault struct{ err error }

func (r refusingVault) Get(context.Context, secret.Ref) (string, error) { return "", r.err }
func (r refusingVault) Set(context.Context, secret.Ref, string) error   { return r.err }
func (r refusingVault) Delete(context.Context, secret.Ref) error        { return r.err }

// reporting is a vault that has already finished opening, which is what every
// test but the one about waiting wants.
func reporting(view ui.VaultView) func(context.Context) (ui.VaultView, error) {
	return func(context.Context) (ui.VaultView, error) { return view, nil }
}

// The property the startup budget rests on: the window is usable before the
// store of the operating system has answered.
//
// Opening a keychain can outlast the whole cold start, so the command starts it
// and hands over a status nobody has yet. Everything that does not need a
// secret has to work anyway — if listing saved connections waited on the vault,
// moving the wait out of the startup would only have moved where it is spent.
func TestTheWindowWorksBeforeTheVaultHasOpened(t *testing.T) {
	t.Parallel()

	stillOpening := make(chan struct{})
	defer close(stillOpening)

	service := ui.NewConnectionService(ui.Dependencies{
		Opener: stubOpener{},
		Store:  &memoryStore{saved: []conn.Config{{ID: "an-id", Host: "h", Port: 5432, User: "u"}}},
		Vault:  secret.NewMemory(),
		VaultStatus: func(context.Context) (ui.VaultView, error) {
			<-stillOpening

			return ui.VaultView{}, nil
		},
	})

	listed, err := service.List()
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(listed) != 1 {
		t.Errorf("List() returned %d connections, want the one that is saved", len(listed))
	}
}

// And when the window does ask, a wait it gave up on comes back as a failure
// rather than as a window that never finishes drawing.
func TestTheVaultStatusGivesUpWithTheWindow(t *testing.T) {
	t.Parallel()

	stillOpening := make(chan struct{})
	defer close(stillOpening)

	service := ui.NewConnectionService(ui.Dependencies{
		Opener: stubOpener{},
		Store:  &memoryStore{},
		Vault:  secret.NewMemory(),
		VaultStatus: func(ctx context.Context) (ui.VaultView, error) {
			select {
			case <-stillOpening:
				return ui.VaultView{}, nil
			case <-ctx.Done():
				return ui.VaultView{}, ctx.Err()
			}
		},
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := service.VaultStatus(ctx); err == nil {
		t.Error("VaultStatus() on a cancelled context = nil, want the failure it gave up with")
	}
}

// blockingVault enters the keychain and stays there, which is what a locked
// login keychain or an unanswered authorisation dialog looks like from here.
type blockingVault struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingVault) block(ctx context.Context) error {
	b.once.Do(func() { close(b.entered) })

	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *blockingVault) Get(ctx context.Context, _ secret.Ref) (string, error) {
	return "", b.block(ctx)
}
func (b *blockingVault) Set(ctx context.Context, _ secret.Ref, _ string) error { return b.block(ctx) }
func (b *blockingVault) Delete(ctx context.Context, _ secret.Ref) error        { return b.block(ctx) }

func newBlockingVault() *blockingVault {
	return &blockingVault{entered: make(chan struct{}), release: make(chan struct{})}
}

// The regression this locking exists to prevent: a save waiting on an
// authorisation dialog used to hold the lock every other call needs, so the
// list the window draws after saving queued behind a dialog nobody had answered
// yet. Reading the saved connections touches no keychain and must not wait for
// one either.
func TestASaveWaitingOnTheKeychainDoesNotHoldUpTheWindow(t *testing.T) {
	t.Parallel()

	blocked := newBlockingVault()
	defer close(blocked.release)

	service := ui.NewConnectionService(ui.Dependencies{
		Opener: stubOpener{},
		Store:  &memoryStore{},
		Vault:  blocked,
	})

	saving := make(chan error, 1)

	go func() {
		_, err := service.Save(t.Context(), ui.ConnectionForm{
			Name: "held up", Host: "h", Port: 5432, User: "u", Password: "s3cr3t",
		})
		saving <- err
	}()

	// Only once the save is actually inside the keychain is the question
	// meaningful; before that it might simply not have got there yet.
	<-blocked.entered

	listed := make(chan error, 1)

	go func() {
		_, err := service.List()
		listed <- err
	}()

	select {
	case err := <-listed:
		if err != nil {
			t.Errorf("List() = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("List() waited for a save that is stuck in the keychain")
	}
}

// And the save itself has to come back. The context Wails hands a bound method
// has no deadline, so a keyring that never answers would otherwise be a button
// that never returns.
func TestASaveGivesUpOnAKeychainThatNeverAnswers(t *testing.T) {
	t.Parallel()

	blocked := newBlockingVault()
	defer close(blocked.release)

	service := ui.NewConnectionService(ui.Dependencies{
		Opener: stubOpener{},
		Store:  &memoryStore{},
		Vault:  blocked,
	})

	ctx, cancel := context.WithCancel(t.Context())

	saving := make(chan error, 1)

	go func() {
		_, err := service.Save(ctx, ui.ConnectionForm{
			Name: "given up", Host: "h", Port: 5432, User: "u", Password: "s3cr3t",
		})
		saving <- err
	}()

	<-blocked.entered
	cancel()

	select {
	case err := <-saving:
		if err == nil {
			t.Error("Save() = nil after the keychain call was given up on")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Save() never came back from a keychain that never answered")
	}
}

// The connection is in the file either way. A password that could not be stored
// is something the person can see and retype; a save that rolled the file back
// because the keychain hung would lose work nobody asked to lose.
func TestAConnectionIsSavedEvenWhenTheKeychainDoesNot(t *testing.T) {
	t.Parallel()

	blocked := newBlockingVault()
	defer close(blocked.release)

	store := &memoryStore{}
	service := ui.NewConnectionService(ui.Dependencies{
		Opener: stubOpener{}, Store: store, Vault: blocked,
	})

	ctx, cancel := context.WithCancel(t.Context())

	go func() {
		<-blocked.entered
		cancel()
	}()

	if _, err := service.Save(ctx, ui.ConnectionForm{
		Name: "kept", Host: "h", Port: 5432, User: "u", Password: "s3cr3t",
	}); err == nil {
		t.Fatal("Save() = nil although the keychain never answered")
	}

	listed, err := service.List()
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "kept" {
		t.Errorf("the saved connections are %+v, want the one that was saved", listed)
	}
}

// The identifier decides where the password is filed, so one the keychain
// cannot address is a connection that can never keep a password. The reference
// refuses a control character; refusing it here is what stops the file being
// written first and the failure arriving second, leaving a saved connection
// that silently cannot hold a credential.
func TestSaveRefusesAnIdentifierTheKeychainCannotAddress(t *testing.T) {
	t.Parallel()

	store := &memoryStore{}
	service := ui.NewConnectionService(ui.Dependencies{
		Opener: stubOpener{}, Store: store, Vault: secret.NewMemory(),
	})

	if _, err := service.Save(t.Context(), ui.ConnectionForm{
		ID: "an\x00id", Name: "broken", Host: "h", Port: 5432, User: "u", Password: "s3cr3t",
	}); err == nil {
		t.Fatal("Save() accepted an identifier the keychain cannot address")
	}

	listed, err := service.List()
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("the connection was written anyway: %+v", listed)
	}
}

// The window carries params and options through so that a pasted URI does not
// lose them. Both are maps of text, and password is a keyword libpq honours, so
// a form arriving with one there is a password on its way to a plain-text file
// and to the connection string after it. It is refused at the boundary, before
// anything is written and before the keychain is touched.
func TestSaveRefusesAPasswordSmuggledThroughTheSettings(t *testing.T) {
	t.Parallel()

	const password = "correct-horse-battery-staple"

	cases := map[string]ui.ConnectionForm{
		"params":  {Params: map[string]string{"password": password}},
		"options": {Options: map[string]string{"sslpassword": password}},
	}

	for name, carrying := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			service, store, vault := saved(t)

			smuggled := form()
			smuggled.Params, smuggled.Options = carrying.Params, carrying.Options

			if _, err := service.Save(t.Context(), smuggled); err == nil {
				t.Fatal("Save() accepted a password under a free-form key")
			}

			if len(store.saved) != 0 {
				t.Errorf("the connection was written anyway: %+v", store.saved)
			}
			if _, err := vault.Get(t.Context(), secret.ConnectionRef(smuggled.ID)); err == nil {
				t.Error("a password reached the keychain for a connection that was refused")
			}
		})
	}
}
