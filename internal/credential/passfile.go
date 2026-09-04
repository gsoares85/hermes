package credential

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The modes the password file and its directory are created with.
//
// 0600 is what libpq itself demands: it refuses to read a password file that
// anyone else can. Saying so here rather than trusting the umask is the
// difference between a permission and a hope.
const (
	fileMode = 0o600
	dirMode  = 0o700
)

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

	path, err := s.write(records)
	if err != nil {
		return Handoff{}, err
	}

	return Handoff{env: []string{passfileVariable + "=" + path}, path: path}, nil
}

func (s *Store) write(records string) (string, error) {
	if err := os.MkdirAll(s.dir, dirMode); err != nil {
		return "", fmt.Errorf("creating %s: %w", s.dir, err)
	}

	// The name carries the process that wrote it, which is what lets the sweep
	// tell a file still in use from one a dead process left behind. See sweep.go.
	file, err := os.CreateTemp(s.dir, filePrefix+strconv.Itoa(os.Getpid())+"-*")
	if err != nil {
		return "", fmt.Errorf("creating a password file in %s: %w", s.dir, err)
	}

	if err := fill(file, records); err != nil {
		_ = os.Remove(file.Name())

		return "", err
	}

	return file.Name(), nil
}

func fill(file *os.File, records string) error {
	defer func() { _ = file.Close() }()

	// CreateTemp already creates the file private to this user on Unix. This is
	// what says so out loud, and what sets it where the default is wider.
	if err := file.Chmod(fileMode); err != nil {
		return fmt.Errorf("setting the permissions of %s: %w", file.Name(), err)
	}

	if _, err := file.WriteString(records); err != nil {
		return fmt.Errorf("writing %s: %w", file.Name(), err)
	}

	// Closed here and not only by the defer: a write that fails on close has
	// still failed, and a caller told it succeeded would hand a child a file
	// that is missing the record it needs.
	if err := file.Close(); err != nil {
		return fmt.Errorf("finishing %s: %w", file.Name(), err)
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
