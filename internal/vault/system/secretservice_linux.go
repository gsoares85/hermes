//go:build linux

package system

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/gsoares85/hermes/internal/core/secret"
)

// How long the tidying-up after a cancelled dialog is allowed to take.
//
// Short, because the agent it talks to is the same one already suspected of
// being stuck. See dismiss.
const dismissTimeout = 2 * time.Second

// Backend names the store to a person, and Advice says what to do when it does
// not answer. Both are prose: they end up in the warning the window shows, and
// somebody has to be able to act on them.
const (
	Backend = "the Secret Service of this desktop session"
	Advice  = "Install and start a keyring — gnome-keyring or kwalletmanager — and start Hermes again."
)

// Names from the Secret Service specification, which is the interface every
// keyring on Linux implements. Hermes speaks it over the session bus rather
// than through the keyring of one particular desktop, so GNOME and KDE are the
// same code path.
const (
	busName     = "org.freedesktop.secrets"
	servicePath = dbus.ObjectPath("/org/freedesktop/secrets")
	// The collection the desktop unlocks at login. Writing to the alias rather
	// than to a named collection is what keeps this working on a session whose
	// default keyring is not called "login".
	collectionPath = dbus.ObjectPath("/org/freedesktop/secrets/aliases/default")

	serviceInterface    = "org.freedesktop.Secret.Service"
	collectionInterface = "org.freedesktop.Secret.Collection"
	itemInterface       = "org.freedesktop.Secret.Item"
	promptInterface     = "org.freedesktop.Secret.Prompt"

	labelProperty      = "org.freedesktop.Secret.Item.Label"
	attributesProperty = "org.freedesktop.Secret.Item.Attributes"

	// noPrompt is the path the specification uses to say that an operation
	// needed no interaction. It is not an object.
	noPrompt = dbus.ObjectPath("/")
)

// secretService stores secrets in the keyring of the desktop session.
//
// The connection to the session bus is opened once, when the vault is opened,
// and held for the life of the application: every call is a round trip, and
// dialling the bus on each one would put a second round trip in front of every
// password.
type secretService struct {
	conn *dbus.Conn
	// session is the transport the keyring hands out for carrying values. The
	// plain algorithm is chosen deliberately: the alternative encrypts the
	// value over a socket that is already restricted to this user, and buys
	// nothing the socket permissions do not already give. localBus is what
	// keeps that argument true — it refuses a bus that is not a socket on this
	// machine, rather than leaving the reasoning to hold by assumption.
	session dbus.ObjectPath
}

// payload mirrors the Secret structure of the specification, which is how a
// value travels to and from the keyring.
type payload struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// Open answers the keyring of this desktop session, and refuses when there is
// none to reach.
func Open(ctx context.Context) (Vault, error) {
	if err := localBus(os.Getenv(busAddressVariable)); err != nil {
		return nil, err
	}

	conn, err := dbus.SessionBusPrivate()
	if err != nil {
		return nil, fmt.Errorf("%w: no session bus: %w", secret.ErrUnavailable, err)
	}

	vault, err := start(ctx, conn)
	if err != nil {
		_ = conn.Close()

		return nil, err
	}

	return vault, nil
}

// The variable the session bus address is read from, by the library and by the
// check below. Named once so the two cannot drift.
const busAddressVariable = "DBUS_SESSION_BUS_ADDRESS"

// Transports of a session bus that stay on this machine.
//
// unixexec starts a program and speaks the protocol over its standard input,
// which never reaches a socket at all.
var localTransports = map[string]bool{"unix": true, "unixexec": true}

// localBus refuses a session bus this password would leave the machine to
// reach.
//
// The session is opened with the "plain" algorithm, and the reason that is
// acceptable is written on the field it fills: the value crosses a Unix socket
// in the runtime directory of this user, which nobody else can open. That
// argument holds for unix: and for nothing else. A container or a forwarded
// desktop with DBUS_SESSION_BUS_ADDRESS set to tcp: would send the password
// across a network in the clear, with the justification quietly gone.
//
// An address that is not set is local by construction: the library then looks
// for the socket of the session, which is a path.
//
// Every transport in the list has to be local, not merely the first. The
// library tries them in order and does not say which one answered, so a list
// with a remote entry in it is a list that could have used it.
//
// Refusing lands in the in-memory fallback, which tells the person in words
// that their passwords are not being kept. That is the honest outcome: the
// alternative is a keyring that works and a secret on the wire.
func localBus(address string) error {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil
	}

	for _, entry := range strings.Split(address, ";") {
		transport, _, _ := strings.Cut(strings.TrimSpace(entry), ":")
		if transport == "" {
			continue
		}

		if !localTransports[transport] {
			return fmt.Errorf(
				"%w: this session bus is reached over %s, and Hermes will not put a password on one in the clear",
				secret.ErrUnavailable, transport)
		}
	}

	return nil
}

// start finishes the handshake and asks the keyring for a session. It is the
// availability probe as much as the setup: a bus with nobody answering on
// org.freedesktop.secrets fails here, and that is a machine with no keyring.
func start(ctx context.Context, conn *dbus.Conn) (Vault, error) {
	if err := conn.Auth(nil); err != nil {
		return nil, fmt.Errorf("%w: authenticating to the session bus: %w", secret.ErrUnavailable, err)
	}
	if err := conn.Hello(); err != nil {
		return nil, fmt.Errorf("%w: greeting the session bus: %w", secret.ErrUnavailable, err)
	}

	var (
		output  dbus.Variant
		session dbus.ObjectPath
	)
	call := conn.Object(busName, servicePath).
		CallWithContext(ctx, serviceInterface+".OpenSession", 0, "plain", dbus.MakeVariant(""))
	if err := call.Store(&output, &session); err != nil {
		return nil, fmt.Errorf("%w: opening a session with %s: %w", secret.ErrUnavailable, busName, err)
	}

	return &secretService{conn: conn, session: session}, nil
}

func (s *secretService) Get(ctx context.Context, ref secret.Ref) (string, error) {
	if err := secret.Usable(ctx, ref); err != nil {
		return "", err
	}

	item, err := s.find(ctx, ref)
	if err != nil {
		return "", err
	}

	var value payload
	call := s.object(item).CallWithContext(ctx, itemInterface+".GetSecret", 0, s.session)
	if err := call.Store(&value); err != nil {
		return "", fmt.Errorf("reading %v from %s: %w", ref, Backend, err)
	}

	return string(value.Value), nil
}

func (s *secretService) Set(ctx context.Context, ref secret.Ref, value string) error {
	if err := secret.Usable(ctx, ref); err != nil {
		return err
	}
	if value == "" {
		return fmt.Errorf("%w: %v", secret.ErrEmptySecret, ref)
	}

	if err := s.unlock(ctx, collectionPath); err != nil {
		return err
	}

	properties := map[string]dbus.Variant{
		labelProperty:      dbus.MakeVariant(ref.String()),
		attributesProperty: dbus.MakeVariant(attributes(ref)),
	}
	stored := payload{
		Session:     s.session,
		Parameters:  []byte{},
		Value:       []byte(value),
		ContentType: "text/plain; charset=utf8",
	}

	var item, prompt dbus.ObjectPath
	// The last argument replaces an item carrying the same attributes instead
	// of filing a second one beside it. Without it, editing a password would
	// leave two entries and the next read would be a coin toss.
	call := s.object(collectionPath).
		CallWithContext(ctx, collectionInterface+".CreateItem", 0, properties, stored, true)
	if err := call.Store(&item, &prompt); err != nil {
		return fmt.Errorf("storing %v in %s: %w", ref, Backend, err)
	}
	if item != noPrompt {
		return nil
	}

	return s.answer(ctx, prompt)
}

func (s *secretService) Delete(ctx context.Context, ref secret.Ref) error {
	if err := secret.Usable(ctx, ref); err != nil {
		return err
	}

	item, err := s.find(ctx, ref)
	if err != nil {
		return err
	}

	var prompt dbus.ObjectPath
	if err := s.object(item).CallWithContext(ctx, itemInterface+".Delete", 0).Store(&prompt); err != nil {
		return fmt.Errorf("deleting %v from %s: %w", ref, Backend, err)
	}

	return s.answer(ctx, prompt)
}

// Close hangs up on the session bus.
func (s *secretService) Close() error {
	if err := s.conn.Close(); err != nil {
		return fmt.Errorf("closing the connection to the session bus: %w", err)
	}

	return nil
}

// find locates the item a reference addresses, unlocking it when the keyring
// has it locked.
func (s *secretService) find(ctx context.Context, ref secret.Ref) (dbus.ObjectPath, error) {
	var unlocked, locked []dbus.ObjectPath
	call := s.service().CallWithContext(ctx, serviceInterface+".SearchItems", 0, attributes(ref))
	if err := call.Store(&unlocked, &locked); err != nil {
		return "", fmt.Errorf("searching %s for %v: %w", Backend, ref, err)
	}

	if len(unlocked) > 0 {
		return unlocked[0], nil
	}
	if len(locked) == 0 {
		return "", fmt.Errorf("%w: %v", secret.ErrNotFound, ref)
	}

	if err := s.unlock(ctx, locked[0]); err != nil {
		return "", err
	}

	return locked[0], nil
}

func (s *secretService) unlock(ctx context.Context, path dbus.ObjectPath) error {
	var (
		unlocked []dbus.ObjectPath
		prompt   dbus.ObjectPath
	)
	call := s.service().CallWithContext(ctx, serviceInterface+".Unlock", 0, []dbus.ObjectPath{path})
	if err := call.Store(&unlocked, &prompt); err != nil {
		return fmt.Errorf("unlocking %s: %w", path, err)
	}
	if len(unlocked) > 0 {
		return nil
	}

	return s.answer(ctx, prompt)
}

// answer drives the dialog the keyring puts in front of an operation and waits
// for the person to deal with it.
//
// The wait is bounded by the context and by nothing else, which is why every
// method of this vault takes one: a locked keyring asks for a password, and a
// call that could not be given up on would be a window frozen behind a dialog
// the person may never have seen.
func (s *secretService) answer(ctx context.Context, prompt dbus.ObjectPath) error {
	if prompt == noPrompt {
		return nil
	}

	match := []dbus.MatchOption{
		dbus.WithMatchObjectPath(prompt),
		dbus.WithMatchInterface(promptInterface),
		dbus.WithMatchMember("Completed"),
	}
	if err := s.conn.AddMatchSignalContext(ctx, match...); err != nil {
		return fmt.Errorf("listening for the answer to %s: %w", prompt, err)
	}
	defer func() { _ = s.conn.RemoveMatchSignal(match...) }()

	completed := make(chan *dbus.Signal, 4)
	s.conn.Signal(completed)
	defer s.conn.RemoveSignal(completed)

	// The empty window identifier lets the keyring place the dialog itself.
	// Hermes has no handle to give it that means the same thing on X11 and on
	// Wayland, and a wrong one puts the dialog behind the window it belongs to.
	if err := s.object(prompt).CallWithContext(ctx, promptInterface+".Prompt", 0, "").Err; err != nil {
		return fmt.Errorf("asking %s for authorisation: %w", prompt, err)
	}

	for {
		select {
		case <-ctx.Done():
			s.dismiss(prompt)

			return fmt.Errorf("giving up on the authorisation dialog: %w", ctx.Err())
		case signal, listening := <-completed:
			// Closing the bus connection closes this channel, and a closed
			// channel answers immediately and for ever. Reading the second
			// value is what tells the two apart: without it, quitting Hermes
			// with a dialog still open turned this loop into a spin consuming
			// a core until the context — which has no deadline of its own —
			// happened to be cancelled.
			if !listening {
				return fmt.Errorf("%w: the session bus closed while %s was on screen",
					secret.ErrUnavailable, prompt)
			}

			if signal == nil || signal.Path != prompt || signal.Name != promptInterface+".Completed" {
				continue
			}

			return completion(signal)
		}
	}
}

// dismiss takes the dialog off the screen after Hermes has given up on it:
// leaving it there asks the person for a password nothing is waiting for any
// more.
//
// It gets a deadline of its own rather than inheriting the one that just
// expired — a cancelled context would refuse the call outright — and it is a
// short one, because the agent that has to answer is the same one already
// suspected of being stuck. A dialog left on screen is untidy; a goroutine
// wedged inside the code that exists to guarantee cancellation is the bug this
// whole path was written to prevent.
func (s *secretService) dismiss(prompt dbus.ObjectPath) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), dismissTimeout)
	defer cancel()

	_ = s.object(prompt).CallWithContext(ctx, promptInterface+".Dismiss", 0).Err
}

func completion(signal *dbus.Signal) error {
	if len(signal.Body) == 0 {
		return fmt.Errorf("%w: the authorisation dialog answered nothing", secret.ErrUnavailable)
	}

	dismissed, ok := signal.Body[0].(bool)
	if !ok {
		return fmt.Errorf("%w: the authorisation dialog answered %T, want a boolean",
			secret.ErrUnavailable, signal.Body[0])
	}
	if dismissed {
		return fmt.Errorf("%w: the authorisation dialog was dismissed", secret.ErrUnavailable)
	}

	return nil
}

// attributes is what an item is found by. The keyring indexes them, so they are
// the reference split into fields — never the secret, which the specification
// would happily let us put here and which every keyring exposes to anything
// allowed to search.
func attributes(ref secret.Ref) map[string]string {
	return map[string]string{"service": ref.Service, "account": ref.Account}
}

func (s *secretService) service() dbus.BusObject { return s.object(servicePath) }

func (s *secretService) object(path dbus.ObjectPath) dbus.BusObject {
	return s.conn.Object(busName, path)
}
