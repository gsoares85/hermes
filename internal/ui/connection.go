package ui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"strings"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/core/secret"
	"github.com/gsoares85/hermes/internal/driver"
)

// vaultTimeout bounds every call this boundary makes to the keychain.
//
// The context Wails hands a bound method carries no deadline of its own, and
// the window offers no way to cancel one, so without this a keyring that never
// answers is a button that never comes back. It is generous rather than tight
// on purpose: on macOS and on Linux the call can be a dialog waiting for a
// person to type their keychain password, and cutting that off after a couple
// of seconds would fail the one case the dialog exists for.
const vaultTimeout = 30 * time.Second

// ConnectionForm is what the window sends when someone describes a connection.
//
// It is the one type that carries a password, and it only ever travels inwards.
// Nothing returned from this service has that field, which is why the form and
// the view below are separate types rather than one shared struct with a rule
// about when to blank a member.
type ConnectionForm struct {
	// ID is empty for a connection being described for the first time and set
	// for one being edited. It decides whether Save adds a connection or
	// replaces one, and it is what the password is filed under in the keychain.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Params are session parameters; Options are libpq connection settings.
	// Both are carried so that a pasted URI does not quietly lose them
	// between the parser and the connection.
	Params  map[string]string `json:"params"`
	Options map[string]string `json:"options"`

	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`

	SSLMode  string `json:"sslMode"`
	RootCert string `json:"rootCert"`
	Cert     string `json:"cert"`
	Key      string `json:"key"`

	// Environment is what this connection is — dev, staging or prod — and
	// empty for one nobody labelled. ReadOnly asks the server to refuse every
	// statement that writes.
	Environment string `json:"environment"`
	ReadOnly    bool   `json:"readOnly"`
}

// String describes the connection without describing the credential.
//
// It exists so that %v cannot print one. ConnectionForm is the only type that
// crosses this boundary carrying a password, so it is the only one that has to
// refuse to render it — and the redaction downstream is a net, not a proof.
func (f ConnectionForm) String() string {
	return fmt.Sprintf("%s@%s:%d/%s", f.User, f.Host, f.Port, f.Database)
}

// LogValue is the same promise for structured logging, where a value is
// rendered field by field rather than as a whole.
func (f ConnectionForm) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", f.ID),
		slog.String("name", f.Name),
		slog.String("host", f.Host),
		slog.Int("port", f.Port),
		slog.String("database", f.Database),
		slog.String("user", f.User),
		slog.String("sslmode", f.SSLMode),
		slog.String("environment", f.Environment),
		slog.Bool("readonly", f.ReadOnly),
		// Whether there is one, never what it is: the difference between a
		// form that will authenticate and one that will not is worth logging.
		slog.Bool("hasPassword", f.Password != ""),
	)
}

// ConnectionView is a connection as the window draws it. There is deliberately
// no password field: a type that cannot carry a secret cannot leak one.
type ConnectionView struct {
	Name    string            `json:"name"`
	Params  map[string]string `json:"params"`
	Options map[string]string `json:"options"`

	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`

	SSLMode  string `json:"sslMode"`
	RootCert string `json:"rootCert"`
	Cert     string `json:"cert"`
	Key      string `json:"key"`

	// HasPassword says the pasted URI carried one, so the form can ask for it
	// rather than pretend the connection is ready.
	HasPassword bool `json:"hasPassword"`
}

// SavedView is a saved connection as the window lists it.
//
// It is a type of its own rather than ConnectionView with an identifier added,
// because the two answer different questions. ConnectionView reports what a
// pasted URI contained, HasPassword included. This reports what is in the file,
// and the file cannot say whether a password is in the keychain: finding out
// would mean reading the keychain once per row, which on macOS and on Linux is
// an authorisation dialog per connection for someone who wanted to see a list.
type SavedView struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Params  map[string]string `json:"params"`
	Options map[string]string `json:"options"`

	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`

	SSLMode  string `json:"sslMode"`
	RootCert string `json:"rootCert"`
	Cert     string `json:"cert"`
	Key      string `json:"key"`

	Environment string `json:"environment"`
	ReadOnly    bool   `json:"readOnly"`

	Archived bool `json:"archived"`
}

// VaultView says where passwords are being kept, and warns when the honest
// answer is "until Hermes quits".
//
// The warning is prose because it is shown to a person and has to name what to
// install. It is empty exactly when the keychain of the system is in use, which
// is what the window decides whether to draw a banner from.
type VaultView struct {
	Backend string `json:"backend"`
	Warning string `json:"warning"`
}

// DiagnosisView is a failure explained, ready to render.
type DiagnosisView struct {
	Failed   bool   `json:"failed"`
	Class    string `json:"class"`
	Summary  string `json:"summary"`
	Cause    string `json:"cause"`
	NextStep string `json:"nextStep"`
	Detail   string `json:"detail"`
}

// StatusView is the state of an open connection, and who that connection is.
//
// The three fields after the state are the ones that make a production server
// unmistakable in every tab. A tab cannot arrange that for itself — it can only
// draw what it was handed — so the label travels with the state rather than
// being looked up separately by whoever remembers to.
type StatusView struct {
	ID        string        `json:"id"`
	State     string        `json:"state"`
	Diagnosis DiagnosisView `json:"diagnosis"`

	Name        string `json:"name"`
	Environment string `json:"environment"`
	ReadOnly    bool   `json:"readOnly"`

	// Where the connection goes, so that a window can say which database and
	// which role it is looking at without asking a second question. None of it
	// is a secret — it is what the form was filled in with, minus the one field
	// that is.
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
}

// ConnectionStore is where saved connections live between runs.
//
// The interface is declared here because this is where it is consumed. What
// satisfies it knows about directories, permissions and atomic renames, and
// none of that belongs in a boundary whose job is to answer a window.
type ConnectionStore interface {
	Load() ([]conn.Config, error)
	Save(connections []conn.Config) error
}

// Dependencies are what the command that wires the application together hands
// to this boundary.
//
// It is a struct rather than four parameters because three of them are
// interfaces, and a positional list of interfaces is a call site nobody can
// read six months later.
type Dependencies struct {
	// Opener is the engine. Naming a concrete one is the job of the command,
	// not of the window.
	Opener driver.Opener
	// Store is where the connections themselves are kept.
	Store ConnectionStore
	// Vault is where their passwords are kept, which is somewhere else.
	Vault secret.Vault
	// VaultStatus answers what to tell the person about that place.
	//
	// A function rather than a value because the answer does not exist yet
	// when the window is built: opening the store of the operating system can
	// outlast the whole startup budget, so the command starts it and the
	// window asks once it is on screen. A nil one reports nothing, which is
	// what a caller with no vault to describe means.
	VaultStatus func(ctx context.Context) (VaultView, error)
}

// ConnectionService is the boundary for everything to do with connecting.
//
// The frontend never assembles a connection string and never sees a password
// come back: it sends a form, and receives a diagnosis or a state.
type ConnectionService struct {
	opener      driver.Opener
	store       ConnectionStore
	vault       secret.Vault
	vaultStatus func(ctx context.Context) (VaultView, error)

	mu   sync.Mutex
	open map[string]*conn.Connection

	// closing is what to tell when a connection goes.
	//
	// A list of functions rather than an interface, and unexported for a
	// reason that is not style: a binding is generated from every exported
	// method of this type, and one taking a callback would cross to the
	// frontend as something it could call. Whoever wires two services together
	// is in this package, which is where the registration belongs.
	closing []func(id string)

	// saved serialises the read-modify-write of the connections file. Two
	// saves at once would otherwise each write the list they read, and the
	// second would delete the connection the first had just added.
	saved sync.Mutex
}

// NewConnectionService creates the service bound to the frontend.
func NewConnectionService(deps Dependencies) *ConnectionService {
	return &ConnectionService{
		opener:      deps.Opener,
		store:       deps.Store,
		vault:       deps.Vault,
		vaultStatus: deps.VaultStatus,
		open:        make(map[string]*conn.Connection),
	}
}

// whenClosed registers something to tell when a connection is released.
//
// It exists because a service built on top of a connection has no other way to
// learn that the connection has gone. The catalog cache found out by accident —
// a later lookup failing — and a lookup for a closed connection never arrives,
// because the window is handed a fresh identifier by every Open.
func (s *ConnectionService) whenClosed(tell func(id string)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closing = append(s.closing, tell)
}

// announceClosed tells everyone registered, outside the lock.
//
// Outside because what is registered is somebody else's code taking somebody
// else's lock, and holding this one across it is how two services that only
// know each other through a callback deadlock.
func (s *ConnectionService) announceClosed(ids ...string) {
	s.mu.Lock()
	listeners := slices.Clone(s.closing)
	s.mu.Unlock()

	for _, tell := range listeners {
		for _, id := range ids {
			tell(id)
		}
	}
}

// VaultStatus says where passwords are being kept.
//
// The window asks so that it can say so, and warn when the answer is that they
// are not being kept at all. A vault that silently forgets is exactly the
// failure this boundary exists to make visible.
//
// It takes a context and can fail because the answer may still be on its way:
// the store of the operating system is opened in the background so that the
// window does not wait for it, and this is the call that waits instead — after
// the window is already drawn, where waiting costs nobody anything.
func (s *ConnectionService) VaultStatus(ctx context.Context) (VaultView, error) {
	if s.vaultStatus == nil {
		return VaultView{}, nil
	}

	// Bounded here like every other call to the keychain, and for a reason this
	// one has of its own: what answers is a function the caller supplied. It
	// happens to be one that cannot hang today, and a guarantee that depends on
	// which function was wired in is not a guarantee this boundary makes.
	ctx, cancel := context.WithTimeout(ctx, vaultTimeout)
	defer cancel()

	view, err := s.vaultStatus(ctx)
	if err != nil {
		return VaultView{}, secret.Error(err)
	}

	return view, nil
}

// Save keeps a connection: the connection in the file, its password in the
// keychain, and never one of them in the other.
//
// A form with no identifier is a new connection and is given one. A form that
// carries one replaces the connection it names, which is what editing is.
func (s *ConnectionService) Save(ctx context.Context, form ConnectionForm) (SavedView, error) {
	config := configOf(form)
	if config.ID == "" {
		config.ID = conn.NewID()
	}
	if err := config.Validate(); err != nil {
		return SavedView{}, secret.Error(err)
	}

	// Checked here, before anything is written. The keychain refuses a
	// reference it cannot address, and finding that out after the file has
	// been saved leaves a connection on disk that can never hold a password —
	// a failure the person sees once, on the save, and never again.
	if err := secret.ConnectionRef(config.ID).Validate(); err != nil {
		return SavedView{}, secret.Error(err)
	}

	stored, err := s.record(config)
	if err != nil {
		return SavedView{}, err
	}

	// After the file and not before it. Either order can fail halfway, and this
	// one fails into a connection whose password has to be typed again — which
	// the person can see and fix — rather than into a password in the keychain
	// belonging to a connection that does not exist, which nothing can reach.
	if err := s.keep(ctx, config); err != nil {
		return SavedView{}, err
	}

	return savedView(stored), nil
}

// record puts the connection in the file and answers it as the file now holds
// it, holding the lock for exactly as long as that takes and no longer.
//
// It answers rather than returning nothing because the file keeps something the
// form does not carry: see replacing below. A caller that reported the form
// back would tell the window a connection had been unarchived when it had not.
//
// The keychain is deliberately outside the lock. A call to the keychain can
// open a dialog and wait for a person, and holding this lock across one would
// put every other call to this service behind that dialog — List above all,
// which the window makes after every save and again at startup.
func (s *ConnectionService) record(config conn.Config) (conn.Config, error) {
	s.saved.Lock()
	defer s.saved.Unlock()

	saved, err := s.store.Load()
	if err != nil {
		return conn.Config{}, secret.Error(err)
	}

	// The store is handed a connection with the password taken out of it. The
	// file format has no field for one and would drop it anyway, but a boundary
	// that relies on the far side to do the dropping is a boundary that leaks
	// the day someone writes a second implementation of it.
	stored := config
	stored.Password = ""

	connections := replacing(saved, stored)
	if err := s.store.Save(connections); err != nil {
		return conn.Config{}, secret.Error(err)
	}

	return connections[indexOf(connections, stored.ID)], nil
}

// indexOf finds the connection replacing just placed. It is always there:
// replacing either overwrites an entry with this identifier or appends one.
func indexOf(connections []conn.Config, id string) int {
	for index, existing := range connections {
		if existing.ID == id {
			return index
		}
	}

	return len(connections) - 1
}

// List returns the saved connections, and touches no keychain doing it.
func (s *ConnectionService) List() ([]SavedView, error) {
	s.saved.Lock()
	defer s.saved.Unlock()

	saved, err := s.store.Load()
	if err != nil {
		return nil, secret.Error(err)
	}

	views := make([]SavedView, 0, len(saved))
	for _, config := range saved {
		views = append(views, savedView(config))
	}

	return views, nil
}

// Delete removes a connection and the password that belonged to it.
//
// Both, because a secret left behind is an item in the keychain of the person
// that nothing will ever look for again: invisible litter that outlives the
// application.
//
// The password goes first, which is the mirror of the order Save uses and the
// same argument. Either order can fail halfway, so the question is which
// half-done state a person can get out of. Removing the connection first fails
// into a secret nothing points at any more — unreachable from here, with no
// second Forget to press, because the connection it belonged to is gone.
// Removing the password first fails into a connection that is still listed and
// still has its Forget button: the operation can simply be repeated, and the
// second attempt finds no secret and says so is normal.
func (s *ConnectionService) Delete(ctx context.Context, id string) error {
	if err := s.discard(ctx, id); err != nil {
		return err
	}

	return s.forget(id)
}

// discard takes the password out of the keychain.
//
// A connection saved without a password has no secret to remove, and that is a
// normal outcome rather than a failure — so is a second Forget after one that
// failed on the file.
func (s *ConnectionService) discard(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, vaultTimeout)
	defer cancel()

	if err := s.vault.Delete(ctx, secret.ConnectionRef(id)); err != nil &&
		!errors.Is(err, secret.ErrNotFound) {
		return fmt.Errorf("the password could not be removed from the keychain, so the connection was kept: %w",
			secret.Error(err))
	}

	return nil
}

// forget takes the connection out of the file, under the same lock and with the
// same boundary as record: the keychain is dealt with by the caller, afterwards.
func (s *ConnectionService) forget(id string) error {
	s.saved.Lock()
	defer s.saved.Unlock()

	saved, err := s.store.Load()
	if err != nil {
		return secret.Error(err)
	}

	remaining, found := without(saved, id)
	if !found {
		return fmt.Errorf("no connection %q is saved", id)
	}

	if err := s.store.Save(remaining); err != nil {
		return secret.Error(err)
	}

	return nil
}

// keep puts the password in the vault, and does nothing when the form carried
// none.
//
// An empty password field is someone editing the host of a connection they
// saved last week, not someone asking for its password to be forgotten. Wiping
// the secret there would lose it to an edit that never mentioned it; forgetting
// a password is what deleting the connection does.
func (s *ConnectionService) keep(ctx context.Context, config conn.Config) error {
	if config.Password == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, vaultTimeout)
	defer cancel()

	if err := s.vault.Set(ctx, secret.ConnectionRef(config.ID), config.Password); err != nil {
		return fmt.Errorf("the connection was saved, but its password was not: %w", secret.Error(err))
	}

	return nil
}

// credentials fills in the password a saved connection is opened with.
//
// This is the only place a stored secret is read, and it is read at the moment
// it is used rather than when the list is drawn. That is the difference between
// opening Hermes and opening Hermes behind one authorisation dialog per saved
// connection, on the two systems where reading an item can raise one.
//
// A saved connection with no secret is not an error: plenty of servers
// authenticate by certificate, by peer, or by a .pgpass the person already has.
func (s *ConnectionService) credentials(ctx context.Context, config conn.Config) (conn.Config, error) {
	if config.Password != "" || strings.TrimSpace(config.ID) == "" {
		return config, nil
	}

	ctx, cancel := context.WithTimeout(ctx, vaultTimeout)
	defer cancel()

	stored, err := s.vault.Get(ctx, secret.ConnectionRef(config.ID))
	if errors.Is(err, secret.ErrNotFound) {
		return config, nil
	}
	if err != nil {
		return config, fmt.Errorf("reading the password of this connection from the keychain: %w",
			secret.Error(err))
	}
	config.Password = stored

	return config, nil
}

// replacing puts the connection in the list, in place of the one it shares an
// identifier with, or at the end when there is none.
//
// The archived flag comes from the entry being replaced rather than from the
// connection handed in, and that is the whole reason this is not an assignment.
// ConnectionForm has no field for it: the window never shows it and never sends
// it, so a form describing an edited host arrives with Archived false whatever
// the file says. Taking the form's word for it would unarchive a connection
// every time somebody changed its port — a value lost in a round trip through a
// screen that never mentioned it.
//
// Archiving is therefore something the file records and something a future
// operation of its own will change. It is not something an edit decides by
// omission.
func replacing(saved []conn.Config, config conn.Config) []conn.Config {
	for index, existing := range saved {
		if existing.ID == config.ID {
			config.Archived = existing.Archived
			saved[index] = config

			return saved
		}
	}

	return append(saved, config)
}

func without(saved []conn.Config, id string) ([]conn.Config, bool) {
	remaining := make([]conn.Config, 0, len(saved))
	found := false

	for _, existing := range saved {
		if existing.ID == id {
			found = true

			continue
		}
		remaining = append(remaining, existing)
	}

	return remaining, found
}

// Parse fills the form from a pasted connection URI.
//
// The password is deliberately left out even when the URI carried one. It came
// from the window a moment ago, so echoing it back leaks nothing new — but it
// would put the secret into the state of a page that is redrawn, inspected and
// occasionally screenshotted, and HasPassword tells the form what it needs to
// know without that.
func (s *ConnectionService) Parse(uri string) (ConnectionView, error) {
	config, err := conn.ParseURI(uri)
	if err != nil {
		return ConnectionView{}, err
	}

	return viewOf(config), nil
}

// Test tries the connection and explains what happened.
//
// It always returns a diagnosis rather than an error: a failure to connect is
// the expected outcome of a test, and the whole point of this task is that the
// window shows something a person can act on instead of a driver message.
func (s *ConnectionService) Test(ctx context.Context, form ConnectionForm) DiagnosisView {
	config, err := s.credentials(ctx, configOf(form))
	if err != nil {
		return diagnosisView(conn.Diagnose(err, config))
	}

	connection, err := conn.Open(ctx, s.opener, config)
	if err != nil {
		return diagnosisView(conn.Diagnose(err, config))
	}
	defer connection.Close()

	return diagnosisView(connection.Check(ctx).Diagnosis)
}

// Open connects and keeps the connection, returning the identifier the window
// uses to refer to it.
func (s *ConnectionService) Open(ctx context.Context, form ConnectionForm) (StatusView, error) {
	config, err := s.credentials(ctx, configOf(form))
	if err != nil {
		return StatusView{}, err
	}

	connection, err := conn.Open(ctx, s.opener, config)
	if err != nil {
		return StatusView{}, secret.Error(err)
	}

	status := connection.Check(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	id, err := newID()
	if err != nil {
		connection.Close()

		return StatusView{}, err
	}
	s.open[id] = connection

	return statusView(id, config, status), nil
}

// Status returns the last known state without contacting the server, which is
// what the window calls as it redraws.
func (s *ConnectionService) Status(id string) (StatusView, error) {
	connection, err := s.lookup(id)
	if err != nil {
		return StatusView{}, err
	}

	return statusView(id, connection.Config(), connection.Status()), nil
}

// Check contacts the server and returns the state it found.
func (s *ConnectionService) Check(ctx context.Context, id string) (StatusView, error) {
	connection, err := s.lookup(id)
	if err != nil {
		return StatusView{}, err
	}

	return statusView(id, connection.Config(), connection.Check(ctx)), nil
}

// Close releases a connection.
func (s *ConnectionService) Close(id string) error {
	s.mu.Lock()
	connection, open := s.open[id]
	delete(s.open, id)
	s.mu.Unlock()

	if !open {
		return fmt.Errorf("no connection %q is open", id)
	}

	connection.Close()
	s.announceClosed(id)

	return nil
}

// ServerVersion answers what the server reports itself to be.
//
// A call of its own rather than a field on the state. Reading the state is a
// read of memory and nothing else — a window repaints far more often than a
// server changes version, and a status read that dialled would turn every
// repaint into a round trip and every unreachable server into a freeze. This
// one reaches the server, so the window asks for it once, when a connection
// opens.
func (s *ConnectionService) ServerVersion(ctx context.Context, id string) (string, error) {
	connection, err := s.lookup(id)
	if err != nil {
		return "", err
	}

	version, err := connection.ServerVersion(ctx)

	return version, secret.Error(err)
}

// Databases lists what the open connection may reach, which is how someone who
// connected without naming a database chooses one.
func (s *ConnectionService) Databases(ctx context.Context, id string) ([]string, error) {
	connection, err := s.lookup(id)
	if err != nil {
		return nil, err
	}

	databases, err := connection.Databases(ctx)

	return databases, secret.Error(err)
}

// Environments lists the labels the form offers, for the same reason SSLModes
// does: a window that retypes the list drifts from what the file will accept,
// and the value that drifts is the one marking a production server.
func (s *ConnectionService) Environments() []string {
	environments := conn.Environments()
	names := make([]string, 0, len(environments))
	for _, environment := range environments {
		names = append(names, string(environment))
	}

	return names
}

// SSLModes lists the modes the form offers, so that the list lives in one place
// rather than being retyped in the window.
func (s *ConnectionService) SSLModes() []string {
	modes := conn.SSLModes()
	names := make([]string, 0, len(modes))
	for _, mode := range modes {
		names = append(names, string(mode))
	}

	return names
}

// CloseAll releases every open connection. The window calls it on shutdown, so
// that reloading or quitting does not leave pools alive until the process dies.
func (s *ConnectionService) CloseAll() {
	s.mu.Lock()
	open := make([]*conn.Connection, 0, len(s.open))
	closed := make([]string, 0, len(s.open))
	for id, connection := range s.open {
		open = append(open, connection)
		closed = append(closed, id)
		delete(s.open, id)
	}
	s.mu.Unlock()

	for _, connection := range open {
		connection.Close()
	}

	s.announceClosed(closed...)
}

// newID returns an unguessable handle. Sequential ones would be fine for what
// they address, but the cost of not having to think about it is one line.
func newID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating a connection identifier: %w", err)
	}

	return hex.EncodeToString(raw), nil
}

func (s *ConnectionService) lookup(id string) (*conn.Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	connection, open := s.open[id]
	if !open {
		return nil, fmt.Errorf("no connection %q is open", id)
	}

	return connection, nil
}

func configOf(form ConnectionForm) conn.Config {
	port := form.Port
	if port == 0 {
		port = conn.DefaultPort
	}

	return conn.Config{
		// Trimmed here rather than only tested trimmed. The identifier is what
		// the password is filed under and what the file is deduplicated on, and
		// profile compares it trimmed — so " a" and "a" are one connection
		// there and were two on the way in. One normalisation, at the boundary
		// the value arrives at, instead of a rule each end has to remember.
		ID:       strings.TrimSpace(form.ID),
		Name:     form.Name,
		Host:     form.Host,
		Port:     port,
		Database: form.Database,
		User:     form.User,
		Password: form.Password,
		TLS: conn.TLS{
			Mode:     conn.SSLMode(form.SSLMode),
			RootCert: form.RootCert,
			Cert:     form.Cert,
			Key:      form.Key,
		},
		Environment: conn.Environment(form.Environment),
		ReadOnly:    form.ReadOnly,
		Params:      form.Params,
		Options:     form.Options,
	}
}

func viewOf(config conn.Config) ConnectionView {
	return ConnectionView{
		Name:        config.Name,
		Host:        config.Host,
		Port:        config.Port,
		Database:    config.Database,
		User:        config.User,
		SSLMode:     string(config.TLS.Mode),
		RootCert:    config.TLS.RootCert,
		Cert:        config.TLS.Cert,
		Key:         config.TLS.Key,
		Params:      config.Params,
		Options:     config.Options,
		HasPassword: config.Password != "",
	}
}

func savedView(config conn.Config) SavedView {
	return SavedView{
		ID:          config.ID,
		Name:        config.Name,
		Host:        config.Host,
		Port:        config.Port,
		Database:    config.Database,
		User:        config.User,
		SSLMode:     string(config.TLS.Mode),
		RootCert:    config.TLS.RootCert,
		Cert:        config.TLS.Cert,
		Key:         config.TLS.Key,
		Params:      config.Params,
		Options:     config.Options,
		Environment: string(config.Environment),
		ReadOnly:    config.ReadOnly,
		Archived:    config.Archived,
	}
}

func diagnosisView(d conn.Diagnosis) DiagnosisView {
	return DiagnosisView{
		Failed:   d.Failed(),
		Class:    string(d.Class),
		Summary:  d.Summary,
		Cause:    d.Cause,
		NextStep: d.NextStep,
		Detail:   d.Detail,
	}
}

func statusView(id string, config conn.Config, status conn.Status) StatusView {
	return StatusView{
		ID:          id,
		State:       string(status.State),
		Diagnosis:   diagnosisView(status.Diagnosis),
		Name:        config.Name,
		Environment: string(config.Environment),
		ReadOnly:    config.ReadOnly,
		Host:        config.Host,
		Port:        config.Port,
		Database:    config.EffectiveDatabase(),
		User:        config.User,
	}
}
