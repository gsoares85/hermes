package sqlitestore_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/core/job"
	"github.com/gsoares85/hermes/internal/core/store"
	"github.com/gsoares85/hermes/internal/core/store/storetest"
	"github.com/gsoares85/hermes/internal/privatedir"
	"github.com/gsoares85/hermes/internal/sqlitestore"
)

// The file answers the same contract as the double the core is tested against.
// This is what the split is worth: every proof written against the in-memory
// history is a proof about this, and the day the driver is replaced the suite
// is already written.
func TestJobHistoryObeysTheContract(t *testing.T) {
	t.Parallel()

	storetest.Run(t, func(t *testing.T) storetest.Open {
		path := filepath.Join(t.TempDir(), "hermes.db")

		return func() (store.JobHistory, error) {
			opened, err := sqlitestore.Open(path)
			if err != nil {
				return nil, err
			}
			t.Cleanup(func() {
				if err := opened.Close(); err != nil {
					t.Errorf("closing the history: %v", err)
				}
			})

			return opened.Jobs(), nil
		}
	})
}

// The reason the file exists, asked of the file: a process that ended left
// something behind, and the next one reads it.
func TestTheHistorySurvivesTheProcessThatWroteIt(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hermes.db")
	written := endedJob("survivor")
	written.Log = []string{"pg_dump: dumping contents of table public.orders"}

	first := opened(t, path)
	if err := first.Jobs().Save(t.Context(), written); err != nil {
		t.Fatalf("saving the job: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("closing the history: %v", err)
	}

	got := recent(t, opened(t, path), store.Page{})
	if len(got) != 1 {
		t.Fatalf("the reopened history holds %d records, want 1", len(got))
	}
	if got[0].ID != written.ID || len(got[0].Log) != 1 {
		t.Errorf("the reopened history holds %+v, want the job that was saved with its log", got[0])
	}
}

// Opening a database twice must not migrate it twice. The ladder is climbed
// from the version the file carries, so a second opening has nothing to do —
// and a step that ran again would fail on a table that already exists, which
// is the failure everyone meets on their second start rather than their first.
func TestOpeningAnExistingDatabaseMigratesNothing(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hermes.db")

	first := opened(t, path)
	if err := first.Close(); err != nil {
		t.Fatalf("closing the database: %v", err)
	}

	again, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatalf("opening the database a second time: %v", err)
	}
	t.Cleanup(func() { _ = again.Close() })

	if got := recent(t, again, store.Page{}); len(got) != 0 {
		t.Errorf("a reopened empty history holds %d records", len(got))
	}
}

// A file from a newer Hermes is refused rather than read. A schema this build
// does not know could be read wrongly instead of not at all, and two versions
// of an application on one machine is a thing people have.
func TestADatabaseFromTheFutureIsRefused(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hermes.db")

	first := opened(t, path)
	if err := first.Close(); err != nil {
		t.Fatalf("closing the database: %v", err)
	}

	ahead, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatalf("reopening the database: %v", err)
	}
	if err := ahead.SetVersion(t.Context(), 99); err != nil {
		t.Fatalf("pretending the file is newer: %v", err)
	}
	if err := ahead.Close(); err != nil {
		t.Fatalf("closing the database: %v", err)
	}

	if _, err := sqlitestore.Open(path); !errors.Is(err, sqlitestore.ErrFromTheFuture) {
		t.Errorf("opening a file from the future returned %v, want ErrFromTheFuture", err)
	}
}

// A file that is not a database is refused with something a person can act on,
// rather than a panic or a history that silently answers nothing.
func TestAFileThatIsNotADatabaseIsRefused(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hermes.db")
	if err := os.WriteFile(path, []byte("this is not a database, it is a note"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	opened, err := sqlitestore.Open(path)
	if err == nil {
		_ = opened.Close()
		t.Fatal("opening a file that is not a database succeeded")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the failure reads %q and does not name the file", err)
	}
}

// The whole of the fallback: a broken file leaves somebody with an application
// that opens, a history that is empty for this session, and a sentence saying
// so. An application that refused to start over its own bookkeeping would be
// choosing that bookkeeping over the backup somebody came to take.
func TestABrokenFileLeavesAWorkingHistoryAndAWarning(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hermes.db")
	if err := os.WriteFile(path, []byte("not a database"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	history := sqlitestore.OpenJobHistory(path)
	t.Cleanup(func() {
		if err := history.Close(); err != nil {
			t.Errorf("closing a history that was never opened: %v", err)
		}
	})

	if history.Warning == "" {
		t.Error("a broken file produced no warning: a history that stopped being kept in silence is worse than one that is gone")
	}
	if !strings.Contains(history.Warning, path) {
		t.Errorf("the warning reads %q and does not name the file", history.Warning)
	}

	// Usable, not merely present. The session still fills a panel; it is the
	// next session that finds nothing.
	if err := history.Jobs.Save(t.Context(), endedJob("after-the-fall")); err != nil {
		t.Fatalf("saving to the history that replaced the broken file: %v", err)
	}

	// And nothing was moved, renamed or deleted: whoever reads the warning
	// still has the file it is about.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the broken file is gone: %v", err)
	}
}

// A file that opens leaves no warning, and the panel shows no sentence about a
// problem nobody has.
func TestAGoodFileLeavesNoWarning(t *testing.T) {
	t.Parallel()

	history := sqlitestore.OpenJobHistory(filepath.Join(t.TempDir(), "hermes.db"))
	t.Cleanup(func() {
		if err := history.Close(); err != nil {
			t.Errorf("closing the history: %v", err)
		}
	})

	if history.Warning != "" {
		t.Errorf("opening a fresh file warned: %q", history.Warning)
	}
	if err := history.Jobs.Save(t.Context(), endedJob("ordinary")); err != nil {
		t.Fatalf("saving a job: %v", err)
	}
}

// The directory is created if it is not there, which is the first run on a
// machine that has never held a Hermes file.
func TestOpeningCreatesTheDirectory(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "never", "been", "here", "hermes.db")

	history := opened(t, path)
	if err := history.Jobs().Save(t.Context(), endedJob("first-run")); err != nil {
		t.Fatalf("saving a job on a first run: %v", err)
	}
}

// The history is a list of the databases somebody operates on, when they did
// it, and what the server said when it went wrong. Nobody else on the machine
// has any business reading it.
func TestTheFileAndItsDirectoryAreTheirOwners(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissions on Windows are an ACL, which internal/privatedir handles and this does not read")
	}

	t.Parallel()

	dir := filepath.Join(t.TempDir(), "hermes")
	path := filepath.Join(dir, "hermes.db")
	opened(t, path)

	file, err := os.Stat(path)
	if err != nil {
		t.Fatalf("looking at the file: %v", err)
	}
	if got := file.Mode().Perm(); got != 0o600 {
		t.Errorf("the file is %v, want 0600", got)
	}

	directory, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("looking at the directory: %v", err)
	}
	if got := directory.Mode().Perm(); got != privatedir.Mode {
		t.Errorf("the directory is %v, want %v", got, privatedir.Mode)
	}
}

// The default path is under the configuration directory of the system, beside
// the connections file rather than somewhere of its own.
func TestDefaultPathIsUnderTheConfigurationDirectory(t *testing.T) {
	t.Parallel()

	path, err := sqlitestore.DefaultPath()
	if err != nil {
		t.Fatalf("finding the default path: %v", err)
	}

	config, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("this system has no configuration directory: %v", err)
	}

	if want := filepath.Join(config, "hermes", "hermes.db"); path != want {
		t.Errorf("the database lives at %q, want %q", path, want)
	}
}

// A row whose state nobody can read is refused with the identifier of the row,
// so that whoever goes looking knows which one to look at.
func TestARowWithAnUnreadableStateIsRefused(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hermes.db")
	history := opened(t, path)

	written := endedJob("unreadable")
	if err := history.Jobs().Save(t.Context(), written); err != nil {
		t.Fatalf("saving the job: %v", err)
	}
	if err := history.WriteState(t.Context(), written.ID, "finito"); err != nil {
		t.Fatalf("writing a state nobody named: %v", err)
	}

	_, err := history.Jobs().Recent(t.Context(), store.Page{})
	if !errors.Is(err, job.ErrNotAState) {
		t.Fatalf("reading a row with an unreadable state returned %v, want ErrNotAState", err)
	}
	if !strings.Contains(err.Error(), written.ID) {
		t.Errorf("the failure reads %q and does not name the row", err)
	}
}

// A log of one empty line and a log of no lines are different things, and the
// encoding has to tell them apart: a line the subprocess wrote and a line
// nobody wrote are not the same evidence.
func TestAnEmptyLineIsNotTheSameAsNoLog(t *testing.T) {
	t.Parallel()

	history := opened(t, filepath.Join(t.TempDir(), "hermes.db")).Jobs()

	blank := endedJob("one-blank-line")
	blank.Log = []string{""}
	silent := endedJob("nothing-said")
	silent.Ended = blank.Ended.Add(-time.Minute)

	for _, record := range []store.JobRecord{blank, silent} {
		if err := history.Save(t.Context(), record); err != nil {
			t.Fatalf("saving %s: %v", record.ID, err)
		}
	}

	got, err := history.Recent(t.Context(), store.Page{})
	if err != nil {
		t.Fatalf("reading the history: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("the history holds %d records, want 2", len(got))
	}
	if len(got[0].Log) != 1 || got[0].Log[0] != "" {
		t.Errorf("the log of a job that said one empty line reads %q, want one empty line", got[0].Log)
	}
	if len(got[1].Log) != 0 {
		t.Errorf("the log of a job that said nothing reads %q, want nothing", got[1].Log)
	}
}

func opened(t *testing.T, path string) *sqlitestore.Store {
	t.Helper()

	history, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	t.Cleanup(func() { _ = history.Close() })

	return history
}

func recent(t *testing.T, history *sqlitestore.Store, page store.Page) []store.JobRecord {
	t.Helper()

	got, err := history.Jobs().Recent(t.Context(), page)
	if err != nil {
		t.Fatalf("reading the history: %v", err)
	}

	return got
}

func endedJob(id string) store.JobRecord {
	return store.JobRecord{
		ID:      id,
		Kind:    "backup",
		Title:   "a database",
		State:   job.Done,
		Started: time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC),
		Ended:   time.Date(2026, time.September, 12, 10, 1, 0, 0, time.UTC),
	}
}

// A path whose parent is a file, which is what a person gets by pointing the
// application at something that is not a directory. It fails with the path in
// the message rather than panicking somewhere inside the driver.
func TestOpeningWhereNoDirectoryCanBeMadeFails(t *testing.T) {
	t.Parallel()

	inTheWay := filepath.Join(t.TempDir(), "hermes")
	if err := os.WriteFile(inTheWay, []byte("a file where a directory should be"), 0o600); err != nil {
		t.Fatalf("writing the file in the way: %v", err)
	}

	path := filepath.Join(inTheWay, "hermes.db")
	if opened, err := sqlitestore.Open(path); err == nil {
		_ = opened.Close()
		t.Fatal("opening a database under a file succeeded")
	}

	// And the fallback covers it, because it is the same failure as any other
	// way of not having a file: a history for this session and a sentence.
	history := sqlitestore.OpenJobHistory(path)
	if history.Warning == "" {
		t.Error("a path that cannot hold a database produced no warning")
	}
	if err := history.Jobs.Save(t.Context(), endedJob("nowhere-to-put-it")); err != nil {
		t.Errorf("the history that replaced the impossible path refuses to be written: %v", err)
	}
}

// Everything a history is asked after its file has been closed is answered
// with a failure that names the file. The window closing while a job is being
// written down is the way this happens, and a panic in that moment would take
// the process with it on the way out.
func TestAClosedHistoryReportsRatherThanPanics(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hermes.db")

	opened, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}

	history := opened.Jobs()
	if err := history.Save(t.Context(), endedJob("before-the-door-shut")); err != nil {
		t.Fatalf("saving a job: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("closing the database: %v", err)
	}

	if err := history.Save(t.Context(), endedJob("after-the-door-shut")); err == nil {
		t.Error("saving to a closed history succeeded")
	}
	if _, err := history.Get(t.Context(), "before-the-door-shut"); err == nil {
		t.Error("reading one job from a closed history succeeded")
	}
	if _, err := history.Recent(t.Context(), store.Page{}); err == nil {
		t.Error("reading a page of a closed history succeeded")
	}
	if _, err := history.Recent(t.Context(), store.Page{After: store.Cursor{Ended: time.Now(), ID: "x"}}); err == nil {
		t.Error("reading a later page of a closed history succeeded")
	}
	if err := history.Forget(t.Context(), "before-the-door-shut"); err == nil {
		t.Error("forgetting in a closed history succeeded")
	}
}
