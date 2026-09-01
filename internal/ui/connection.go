package ui

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/driver"
)

// ConnectionForm is what the window sends when someone describes a connection.
//
// It is the one type that carries a password, and it only ever travels inwards.
// Nothing returned from this service has that field, which is why the form and
// the view below are separate types rather than one shared struct with a rule
// about when to blank a member.
type ConnectionForm struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`

	SSLMode  string `json:"sslMode"`
	RootCert string `json:"rootCert"`
	Cert     string `json:"cert"`
	Key      string `json:"key"`
}

// ConnectionView is a connection as the window draws it. There is deliberately
// no password field: a type that cannot carry a secret cannot leak one.
type ConnectionView struct {
	Name     string `json:"name"`
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

// DiagnosisView is a failure explained, ready to render.
type DiagnosisView struct {
	Failed   bool   `json:"failed"`
	Class    string `json:"class"`
	Summary  string `json:"summary"`
	Cause    string `json:"cause"`
	NextStep string `json:"nextStep"`
	Detail   string `json:"detail"`
}

// StatusView is the state of an open connection.
type StatusView struct {
	ID        string        `json:"id"`
	State     string        `json:"state"`
	Diagnosis DiagnosisView `json:"diagnosis"`
}

// ConnectionService is the boundary for everything to do with connecting.
//
// The frontend never assembles a connection string and never sees a password
// come back: it sends a form, and receives a diagnosis or a state.
type ConnectionService struct {
	opener driver.Opener

	mu     sync.Mutex
	open   map[string]*conn.Connection
	nextID int
}

// NewConnectionService creates the service bound to the frontend.
func NewConnectionService(opener driver.Opener) *ConnectionService {
	return &ConnectionService{opener: opener, open: make(map[string]*conn.Connection)}
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
	config := configOf(form)

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
	config := configOf(form)

	connection, err := conn.Open(ctx, s.opener, config)
	if err != nil {
		return StatusView{}, err
	}

	status := connection.Check(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	id := strconv.Itoa(s.nextID)
	s.open[id] = connection

	return statusView(id, status), nil
}

// Status returns the last known state without contacting the server, which is
// what the window calls as it redraws.
func (s *ConnectionService) Status(id string) (StatusView, error) {
	connection, err := s.lookup(id)
	if err != nil {
		return StatusView{}, err
	}

	return statusView(id, connection.Status()), nil
}

// Check contacts the server and returns the state it found.
func (s *ConnectionService) Check(ctx context.Context, id string) (StatusView, error) {
	connection, err := s.lookup(id)
	if err != nil {
		return StatusView{}, err
	}

	return statusView(id, connection.Check(ctx)), nil
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

	return nil
}

// Databases lists what the open connection may reach, which is how someone who
// connected without naming a database chooses one.
func (s *ConnectionService) Databases(ctx context.Context, id string) ([]string, error) {
	connection, err := s.lookup(id)
	if err != nil {
		return nil, err
	}

	return connection.Databases(ctx)
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
		HasPassword: config.Password != "",
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

func statusView(id string, status conn.Status) StatusView {
	return StatusView{
		ID:        id,
		State:     string(status.State),
		Diagnosis: diagnosisView(status.Diagnosis),
	}
}
