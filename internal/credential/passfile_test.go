package credential_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/credential"
)

func target() credential.Target {
	return credential.Target{
		Host:     "db.example.com",
		Port:     5432,
		Database: "app",
		User:     "reader",
		Password: password,
	}
}

// passfileOf returns the path the handoff points the child at, and fails when
// it points at nothing.
func passfileOf(t *testing.T, handoff credential.Handoff) string {
	t.Helper()

	for _, entry := range environmentOf(t, handoff) {
		if path, found := strings.CutPrefix(entry, "PGPASSFILE="); found {
			return path
		}
	}

	t.Fatalf("the handoff carries no PGPASSFILE: %v", environmentOf(t, handoff))

	return ""
}

func TestInFileWritesTheRecordLibpqReads(t *testing.T) {
	t.Parallel()

	store := credential.NewStore(t.TempDir())

	handoff, err := store.InFile(target())
	if err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}
	defer func() { _ = handoff.Release() }()

	content, err := os.ReadFile(passfileOf(t, handoff))
	if err != nil {
		t.Fatalf("reading the password file: %v", err)
	}

	const want = "db.example.com:5432:app:reader:s3cr3t\n"
	if string(content) != want {
		t.Errorf("the password file holds %q, want %q", content, want)
	}
}

// libpq splits a record on colons and reads a backslash as an escape, so a
// password holding either character has to arrive escaped — otherwise it is
// read as a shorter password and a field that was never there.
func TestInFileEscapesWhatWouldEndAField(t *testing.T) {
	t.Parallel()

	store := credential.NewStore(t.TempDir())

	awkward := target()
	awkward.Password = `pa:ss\word`
	awkward.User = "read:er"

	handoff, err := store.InFile(awkward)
	if err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}
	defer func() { _ = handoff.Release() }()

	content, err := os.ReadFile(passfileOf(t, handoff))
	if err != nil {
		t.Fatalf("reading the password file: %v", err)
	}

	const want = `db.example.com:5432:app:read\:er:pa\:ss\\word` + "\n"
	if string(content) != want {
		t.Errorf("the password file holds %q, want %q", content, want)
	}
}

// The reason the file exists at all: one operation, more than one server, and
// an environment that has room for exactly one password.
func TestInFileWritesOneRecordPerTarget(t *testing.T) {
	t.Parallel()

	store := credential.NewStore(t.TempDir())

	source := target()
	destination := target()
	destination.Host = "replica.example.com"
	destination.Password = "0th3r"

	handoff, err := store.InFile(source, destination)
	if err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}
	defer func() { _ = handoff.Release() }()

	content, err := os.ReadFile(passfileOf(t, handoff))
	if err != nil {
		t.Fatalf("reading the password file: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("the password file holds %q, want one record per target", content)
	}
	if !strings.HasPrefix(lines[1], "replica.example.com:") {
		t.Errorf("the password file holds %q, want the targets in the order they were given", content)
	}
}

// A target with no password has nothing to hand over, and a record with an
// empty password field would match the server and then fail to authenticate —
// worse than no record, which lets libpq go on to the person's own .pgpass.
func TestInFileSkipsATargetWithNoPassword(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store := credential.NewStore(directory)

	without := target()
	without.Password = ""

	handoff, err := store.InFile(without)
	if err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}

	if got := environmentOf(t, handoff, "PATH=/usr/bin"); !slices.Equal(got, []string{"PATH=/usr/bin"}) {
		t.Errorf("environment = %v, want it untouched: there was no password to hand over", got)
	}
	if entries, _ := os.ReadDir(directory); len(entries) != 0 {
		t.Errorf("%d files were written for a target with no password", len(entries))
	}
}

// A record libpq cannot match is a record that does nothing but keep a password
// on disk, so an incomplete target is refused before anything is written.
func TestInFileRefusesATargetItCouldNotMatch(t *testing.T) {
	t.Parallel()

	cases := map[string]func(*credential.Target){
		"no host":       func(target *credential.Target) { target.Host = "" },
		"no port":       func(target *credential.Target) { target.Port = 0 },
		"a wild port":   func(target *credential.Target) { target.Port = 70000 },
		"no database":   func(target *credential.Target) { target.Database = "" },
		"no user":       func(target *credential.Target) { target.User = "" },
		"a line break":  func(target *credential.Target) { target.Password = "two\nlines" },
		"a broken host": func(target *credential.Target) { target.Host = "db\nexample" },
	}

	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			directory := t.TempDir()
			broken := target()
			damage(&broken)

			if _, err := credential.NewStore(directory).InFile(broken); err == nil {
				t.Fatalf("InFile(%+v) wrote a record that libpq could not use", broken)
			}
			if entries, _ := os.ReadDir(directory); len(entries) != 0 {
				t.Errorf("%d files were left behind by a call that failed", len(entries))
			}
		})
	}
}

func TestReleaseRemovesTheFile(t *testing.T) {
	t.Parallel()

	store := credential.NewStore(t.TempDir())

	handoff, err := store.InFile(target())
	if err != nil {
		t.Fatalf("InFile(...) = %v", err)
	}
	path := passfileOf(t, handoff)

	if err := handoff.Release(); err != nil {
		t.Fatalf("Release() = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the password file is still there after Release: %v", err)
	}

	// A deferred Release after an explicit one, and a sweep that got there
	// first, are both ordinary. Neither is a failure.
	if err := handoff.Release(); err != nil {
		t.Errorf("the second Release() = %v, want nil", err)
	}
}

// The file goes under the cache directory of the operating system, in a
// directory of ours: a place that belongs to this user on all three platforms.
func TestDefaultDirIsUnderTheCacheDirectoryOfTheUser(t *testing.T) {
	t.Parallel()

	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("this system has no cache directory: %v", err)
	}

	got, err := credential.DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir() = %v", err)
	}

	if !strings.HasPrefix(got, cache+string(filepath.Separator)) {
		t.Errorf("DefaultDir() = %q, want it under %q", got, cache)
	}
	if !strings.Contains(got, "hermes") {
		t.Errorf("DefaultDir() = %q, want it in a directory of ours", got)
	}
}
