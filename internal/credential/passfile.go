package credential

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gsoares85/hermes/internal/privatedir"
)

// The mode the password file is created with. Its directory is
// privatedir.Make's business.
//
// 0600 is what libpq itself demands: it refuses to read a password file that
// anyone else can. Saying so here rather than trusting the umask is the
// difference between a permission and a hope.
const fileMode = 0o600

// Target is one server a child process will have to authenticate to.
//
// The four fields before the password are not decoration: libpq matches a
// record by all of them, so a record with the wrong host is a password sitting
// on disk for nothing.
type Target struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
}

// String describes the target without describing the credential, so that a
// caller working out why a dump failed can log what it was talking to.
func (t Target) String() string {
	return fmt.Sprintf("%s@%s:%d/%s", t.User, t.Host, t.Port, t.Database)
}

// LogValue is the same promise for structured logging.
func (t Target) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("host", t.Host),
		slog.Int("port", t.Port),
		slog.String("database", t.Database),
		slog.String("user", t.User),
		slog.Bool("hasPassword", t.Password != ""),
	)
}

// Store is the directory temporary password files are written to.
//
// The directory is given rather than found, so that a test can point it
// somewhere harmless and so that the sweep and the writing agree on one place
// without a global.
type Store struct {
	dir string
}

// NewStore builds the store for a directory.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// DefaultDir is where the files go when nobody has said otherwise: the cache
// directory of the operating system, under a directory of ours.
//
// The cache directory and not the configuration directory, because these files
// are disposable by definition — anything that survives a reboot in here is
// litter, and a cache is the one place a system already expects to hold litter.
func DefaultDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("finding the cache directory of this system: %w", err)
	}

	return filepath.Join(dir, "hermes", "credentials"), nil
}

// InFile writes a password file for these targets and points the child at it.
//
// It is the exception, not the default: it is for an operation that reaches
// more than one server, where the single password the environment can carry is
// not enough. Targets with no password contribute no record, and a call where
// none of them has one writes nothing at all — an empty file would only stop
// libpq from going on to the person's own .pgpass.
func (s *Store) InFile(targets ...Target) (Handoff, error) {
	records, err := records(targets)
	if err != nil {
		return Handoff{}, err
	}
	if records == "" {
		return Handoff{}, nil
	}

	path, claim, err := s.write(records)
	if err != nil {
		return Handoff{}, err
	}

	return Handoff{env: []string{passfileVariable + "=" + path}, path: path, claim: claim}, nil
}

// write creates the password file and answers the claim on it, which is what
// tells every sweep on this machine that an operation is using it and what the
// operating system takes back when this process ends however it ends.
func (s *Store) write(records string) (string, io.Closer, error) {
	if err := privatedir.Make(s.dir); err != nil {
		return "", nil, err
	}

	// The name carries the process that wrote it. It is a label, not evidence:
	// ReleaseAll uses it to find this run's own files, and the sweep asks the
	// operating system instead of believing it. See sweep.go.
	file, err := os.CreateTemp(s.dir, filePrefix+strconv.Itoa(os.Getpid())+"-*")
	if err != nil {
		return "", nil, fmt.Errorf("creating a password file in %s: %w", s.dir, err)
	}
	defer func() { _ = file.Close() }()

	// Claimed before a single byte is written. Between the file appearing in
	// the directory and the claim being taken there is a moment in which a
	// sweep running at that instant would collect it, and taking the claim here
	// keeps that moment down to a couple of system calls over an empty file.
	// The most it can cost is this operation failing to authenticate; it cannot
	// leave a secret behind, because there is no secret in the file yet.
	claim, err := hold(file)
	if err != nil {
		_ = os.Remove(file.Name())

		return "", nil, err
	}

	if err := fill(file, records); err != nil {
		_ = claim.Close()
		_ = os.Remove(file.Name())

		return "", nil, err
	}

	return file.Name(), claim, nil
}

// fill writes the records and flushes them. A child process reads this file
// through a handle of its own, and what it reads has to be all of it.
func fill(file *os.File, records string) error {
	// CreateTemp already creates the file private to this user on Unix. This is
	// what says so out loud, and what sets it where the default is wider.
	if err := file.Chmod(fileMode); err != nil {
		return fmt.Errorf("setting the permissions of %s: %w", file.Name(), err)
	}

	if _, err := file.WriteString(records); err != nil {
		return fmt.Errorf("writing %s: %w", file.Name(), err)
	}

	if err := file.Sync(); err != nil {
		return fmt.Errorf("flushing %s: %w", file.Name(), err)
	}

	return nil
}

// Inside a record, a backslash escapes and a colon separates, so a field
// holding either has to arrive escaped — otherwise libpq reads a shorter
// password and a field that was never there.
var escapeField = strings.NewReplacer(`\`, `\\`, `:`, `\:`)

func records(targets []Target) (string, error) {
	var file strings.Builder

	for _, target := range targets {
		if target.Password == "" {
			continue
		}
		if err := target.validate(); err != nil {
			return "", err
		}

		file.WriteString(record(target))
		file.WriteString("\n")
	}

	return file.String(), nil
}

func record(target Target) string {
	return strings.Join([]string{
		escapeField.Replace(target.Host),
		strconv.Itoa(target.Port),
		escapeField.Replace(target.Database),
		escapeField.Replace(target.User),
		escapeField.Replace(target.Password),
	}, ":")
}

func (t Target) validate() error {
	fields := []struct{ name, value string }{
		{"host", t.Host},
		{"database", t.Database},
		{"user", t.User},
	}

	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%w: no %s", ErrIncompleteTarget, field.name)
		}
		if err := representable(field.name, field.value); err != nil {
			return err
		}
	}

	if t.Port <= 0 || t.Port > 65535 {
		return fmt.Errorf("%w: %d is not a port", ErrIncompleteTarget, t.Port)
	}

	return representable("password", t.Password)
}
