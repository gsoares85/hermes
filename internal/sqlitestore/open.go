package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	// The driver, registered under the name Open asks for below. Blank because
	// nothing here calls it directly: this is the one import in the repository
	// that makes SQLite a fact, and the gate keeps it that way.
	_ "modernc.org/sqlite"

	"github.com/gsoares85/hermes/internal/core/store"
	"github.com/gsoares85/hermes/internal/privatedir"
)

// The name of the file, under the configuration directory of the system. One
// file for everything the application remembers: the job history today, the
// backup catalogue and the preferences next, each behind a contract of its own
// and all sharing one migration ladder.
const fileName = "hermes.db"

// The permissions of the file. A history is a list of the databases somebody
// operates on, the moments they did it and what the server said when it went
// wrong; the directory is already theirs alone, and this says the same about
// the file for the case where it is not.
const fileMode = 0o600

// How long a statement waits for another process to let go of the file.
//
// Two windows of Hermes on one machine share this file, and SQLite gives a
// writer the whole database. Five seconds is long enough for the other one to
// finish writing a row and short enough that nobody thinks the application has
// stopped — and nothing here writes more than a row at a time.
const busyTimeout = 5 * time.Second

// ErrFromTheFuture is a file written by a version of Hermes newer than this
// one.
//
// Refused rather than used, and this is the one thing a broken file and a
// future one are not treated alike for: a schema this build does not know
// could be read wrongly rather than not at all, and a newer Hermes on the same
// machine is a thing people have.
var ErrFromTheFuture = errors.New("the local database was written by a newer version of Hermes")

// ErrDamagedVersion is a file whose schema version is not a version at all.
//
// The version lives in four bytes of the SQLite header and it is a signed
// integer, so a flipped bit makes it negative — a file that has been damaged
// rather than one written by another build. It is refused for the same reason
// as a file that is not a database: what a step of the ladder would do to it
// cannot be reasoned about.
var ErrDamagedVersion = errors.New("the local database has a damaged schema version")

// ErrPathIsNotADSN is a path the driver would read as settings.
//
// Everything after the first question mark in what the driver is handed
// configures the connection rather than naming a file, and it splits on one
// before the file is opened. A path carrying one would open a different file
// from the one it names, with pragmas nobody chose — so it is refused here,
// where the path is still a path.
var ErrPathIsNotADSN = errors.New("the path of the local database cannot contain a question mark")

// dsn is the file as the driver is asked for it.
//
// The wait for a file another process is writing goes here rather than into a
// statement, because a statement reaches the one connection it runs on. The
// pool is capped at one connection, which is not the same as one connection
// for ever: database/sql replaces a connection it finds broken, and the
// replacement would arrive with no wait at all and give up on the first lock
// it met. In the name, every connection the driver makes is born with it.
//
// The path carries no query of its own — Open refuses one — so what follows
// the question mark here is unambiguous.
func dsn(path string) string {
	return fmt.Sprintf("%s?_pragma=busy_timeout(%d)", path, busyTimeout.Milliseconds())
}

// Store is the local database: one file, and everything kept in it.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens the database at path, creating and migrating it as needed.
func Open(path string) (*Store, error) {
	if strings.ContainsRune(path, '?') {
		return nil, fmt.Errorf("%w: %s", ErrPathIsNotADSN, path)
	}

	if err := privatedir.Make(filepath.Dir(path)); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	// One connection, so that writers never queue behind each other inside
	// this process and a pragma set once stays set. The load this carries is a
	// row per operation and a page per panel; a pool would be answering a
	// question nobody has asked.
	db.SetMaxOpenConns(1)

	opened := &Store{db: db, path: path}
	if err := opened.prepare(context.Background()); err != nil {
		_ = db.Close()

		return nil, err
	}

	return opened, nil
}

// prepare puts the connection in the state the rest of the package assumes,
// and brings the schema up to date.
//
// Opening a database/sql handle opens no file: everything that can be wrong
// with the path, the permissions or the bytes is found here, at the first
// statement, which is why this runs before Open answers rather than at the
// first save.
func (s *Store) prepare(ctx context.Context) error {
	// Before anything is written into it. The file is created by the first
	// statement, and a file created with the umask of the session is a file
	// that is readable by others for as long as it takes to get here.
	if _, err := s.db.ExecContext(ctx, "PRAGMA user_version"); err != nil {
		return fmt.Errorf("opening %s: %w", s.path, err)
	}

	if err := s.narrow(); err != nil {
		return err
	}

	// Written into the file rather than set on a connection: the journal mode
	// is a property of the database and survives in it, so saying it once is
	// saying it for good. The wait for a busy file is the opposite — it
	// belongs to a connection — and that one is in the name the driver was
	// opened with, so every connection it makes is born with it.
	if _, err := s.db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		return fmt.Errorf("preparing %s: %w", s.path, err)
	}

	// Again, because the write-ahead log and its index are created by that
	// first pragma and they are where the newest rows live until a checkpoint
	// moves them across. A database nobody else can read whose recent history
	// anybody can read is not a database nobody else can read.
	if err := s.narrow(); err != nil {
		return err
	}

	return s.migrate(ctx)
}

// narrow makes the file and the two SQLite keeps beside it private to their
// owner.
//
// The two are the write-ahead log and its shared index. They come and go —
// SQLite removes them when the last connection closes cleanly — so one that is
// not there is not a failure, it is a database at rest.
func (s *Store) narrow() error {
	for _, path := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		if err := os.Chmod(path, fileMode); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("setting the permissions of %s: %w", path, err)
		}
	}

	return nil
}

// Close releases the file.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", s.path, err)
	}

	return nil
}

// Jobs answers the history of jobs kept in this database.
func (s *Store) Jobs() *JobHistory {
	return &JobHistory{db: s.db, path: s.path}
}

// DefaultPath is where the database lives when nobody has said otherwise: the
// configuration directory of the operating system, under a directory of ours —
// the same one the connections file is in.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding the configuration directory of this system: %w", err)
	}

	return filepath.Join(dir, "hermes", fileName), nil
}

// Opened is a history to use, whatever state the file turned out to be in.
//
// It exists because losing the history is not a reason to refuse to start. A
// file somebody's backup software restored half way, a disk that filled while
// a row was being written, a path that is suddenly a directory: none of that
// stops a person taking a backup, and an application that would not open
// because of it would be choosing its own bookkeeping over their work. The
// same argument the vault makes for falling back to a store that lives in the
// process, and the same answer — say so, out loud, and carry on.
type Opened struct {
	// Jobs is the history, whether it is the file or the one in this process.
	Jobs store.JobHistory
	// Warning is what to put in front of the person, and is empty when there
	// is nothing to say. A history that silently stopped being kept is worse
	// than one that is gone, because nobody finds out until they look for
	// something that should be there.
	Warning string

	closer *Store
}

// OpenJobHistory answers a job history to use, and never fails.
//
// A file that cannot be opened leaves the person with a history that lives in
// this process and dies with it, which is empty now and will not be there
// tomorrow. Nothing is moved, renamed or deleted: the file stays exactly as it
// is, so that whoever looks into the warning still has it to look at, and so
// that a path that failed for a reason that passes — a disk momentarily full,
// a directory not yet mounted — works again by itself on the next start.
func OpenJobHistory(path string) Opened {
	opened, err := Open(path)
	if err != nil {
		return Opened{
			Jobs:    store.NewJobMemory(),
			Warning: fmt.Sprintf("the job history at %s could not be opened, so this session will not be remembered: %v", path, err),
		}
	}

	return Opened{Jobs: opened.Jobs(), closer: opened}
}

// Close releases the file, and does nothing when there was none.
func (o Opened) Close() error {
	if o.closer == nil {
		return nil
	}

	return o.closer.Close()
}
