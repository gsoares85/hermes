package filestore_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/filestore"
)

func TestAFileThatIsNotThereIsAnEmptyList(t *testing.T) {
	t.Parallel()

	store := filestore.NewConnections(filepath.Join(t.TempDir(), "never-written.toml"))

	saved, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v, want no error on a first run", err)
	}
	if len(saved) != 0 {
		t.Errorf("Load() returned %d connections from a file that does not exist", len(saved))
	}
}

func TestSavedConnectionsComeBack(t *testing.T) {
	t.Parallel()

	store := filestore.NewConnections(filepath.Join(t.TempDir(), "hermes", "connections.toml"))
	saved := []conn.Config{sample("first"), sample("second")}

	if err := store.Save(saved); err != nil {
		t.Fatalf("Save() = %v", err)
	}

	read, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if len(read) != 2 || read[0].Name != "first" || read[1].Name != "second" {
		t.Errorf("Load() = %+v, want the two connections that were saved", read)
	}
}

// Saving is how the list changes, so the second write has to replace the first
// rather than add to it.
func TestSavingReplacesTheWholeList(t *testing.T) {
	t.Parallel()

	store := filestore.NewConnections(filepath.Join(t.TempDir(), "connections.toml"))

	if err := store.Save([]conn.Config{sample("first"), sample("second")}); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	if err := store.Save([]conn.Config{sample("only")}); err != nil {
		t.Fatalf("Save() = %v", err)
	}

	read, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if len(read) != 1 || read[0].Name != "only" {
		t.Errorf("Load() = %+v, want only the connection saved last", read)
	}
}

// The file names every host someone can reach and the user they reach it as.
// That is worth keeping to the account that owns it.
func TestTheFileIsPrivateToItsOwner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits do not describe access on Windows, where the " +
			"configuration directory of the user carries the access control instead")
	}
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hermes", "connections.toml")
	store := filestore.NewConnections(path)

	if err := store.Save([]conn.Config{sample("first")}); err != nil {
		t.Fatalf("Save() = %v", err)
	}

	file, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if mode := file.Mode().Perm(); mode != 0o600 {
		t.Errorf("the file is %o, want 600", mode)
	}

	directory, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat %s: %v", filepath.Dir(path), err)
	}
	if mode := directory.Mode().Perm(); mode != 0o700 {
		t.Errorf("the directory is %o, want 700", mode)
	}
}

// The replacement is a rename, so a failure part of the way through leaves the
// previous list where it was. What must never happen is a temporary file left
// behind beside it, which is both litter and a second copy of the list.
func TestSavingLeavesNothingBesideTheFile(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store := filestore.NewConnections(filepath.Join(directory, "connections.toml"))

	for range 3 {
		if err := store.Save([]conn.Config{sample("first")}); err != nil {
			t.Fatalf("Save() = %v", err)
		}
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("reading %s: %v", directory, err)
	}
	if len(entries) != 1 || entries[0].Name() != "connections.toml" {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("the directory holds %v, want only the connections file", names)
	}
}

// A file someone has broken by hand is reported with its path, because the
// message is useless without it: the point of the line number the format
// reports is that there is a file to go and open.
func TestABrokenFileIsReportedWithItsPath(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "connections.toml")
	if err := os.WriteFile(path, []byte("version = 9\n"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	_, err := filestore.NewConnections(path).Load()
	if err == nil {
		t.Fatal("Load() = nil, want the unknown version to be reported")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error does not name the file: %v", err)
	}
}

func TestTheDefaultPathIsUnderTheConfigurationDirectory(t *testing.T) {
	t.Parallel()

	path, err := filestore.ConnectionsPath()
	if err != nil {
		t.Fatalf("ConnectionsPath() = %v", err)
	}

	if base := filepath.Base(path); base != "connections.toml" {
		t.Errorf("the file is called %q, want connections.toml", base)
	}
	if directory := filepath.Base(filepath.Dir(path)); directory != "hermes" {
		t.Errorf("the file sits in %q, want a directory of ours", directory)
	}
}

func sample(name string) conn.Config {
	return conn.Config{
		ID:       conn.NewID(),
		Name:     name,
		Host:     "db.example.com",
		Port:     5432,
		Database: "app",
		User:     "reader",
		TLS:      conn.TLS{Mode: conn.SSLVerifyFull},
	}
}

// A path whose parent is a file and not a directory is what a person produces
// by pointing the configuration directory somewhere odd. It has to be reported
// rather than panic or, worse, silently succeed at writing nothing.
func TestAPathThatCannotHoldAFileIsReported(t *testing.T) {
	t.Parallel()

	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("in the way"), 0o600); err != nil {
		t.Fatalf("writing the file in the way: %v", err)
	}

	store := filestore.NewConnections(filepath.Join(blocked, "connections.toml"))

	if err := store.Save([]conn.Config{sample("first")}); err == nil {
		t.Error("Save() = nil, want the directory that cannot be created to be reported")
	}
}

// A connection with no identifier never reaches the file: the format refuses it
// because its password could never be found again. The store passes that
// refusal through rather than writing a file it will not be able to read.
func TestSavingAConnectionTheFormatRefusesFails(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "connections.toml")
	store := filestore.NewConnections(path)

	unsaved := sample("first")
	unsaved.ID = ""

	if err := store.Save([]conn.Config{unsaved}); err == nil {
		t.Fatal("Save() = nil, want a connection with no identifier to be refused")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a refused save left a file behind")
	}
}
