//go:build integration

package ui_test

import (
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
	"github.com/gsoares85/hermes/internal/core/ddl"
	"github.com/gsoares85/hermes/internal/core/secret"
	"github.com/gsoares85/hermes/internal/driver/postgres"
	"github.com/gsoares85/hermes/internal/testsupport"
	"github.com/gsoares85/hermes/internal/ui"
)

// realTree opens a connection to a server through the real service and answers
// the tree over it, with the corpus schema it can be pointed at.
func realTree(t *testing.T) (*ui.CatalogService, string, string, *testsupport.Instance) {
	t.Helper()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])
	corpus := testsupport.Corpus(t, instance)

	connections := ui.NewConnectionService(ui.Dependencies{
		Opener: postgres.New(),
		Store:  &memoryStore{},
		Vault:  secret.NewMemory(),
	})
	t.Cleanup(connections.CloseAll)

	status, err := connections.Open(t.Context(), formFor(t, instance))
	if err != nil {
		t.Fatalf("opening the connection: %v", err)
	}

	return ui.NewCatalogService(connections), status.ID, corpus, instance
}

// formFor turns the container's DSN into the form the window would have sent.
func formFor(t *testing.T, instance *testsupport.Instance) ui.ConnectionForm {
	t.Helper()

	parsed, err := url.Parse(instance.DSN)
	if err != nil {
		t.Fatalf("parsing the DSN: %v", err)
	}

	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatalf("reading the port out of %s: %v", instance.DSN, err)
	}

	return ui.ConnectionForm{
		Host:     parsed.Hostname(),
		Port:     port,
		Database: testsupport.Database,
		User:     testsupport.User,
		Password: testsupport.Password,
		SSLMode:  "disable",
	}
}

// The DDL the panel shows is the DDL the generator writes.
//
// It is the acceptance criterion of the screen and it is worth a server: the
// failure it rules out is a second generator growing inside the window, which
// would look authoritative and drift from the one the diff and the sync are
// built on. So the answer is compared with what the generator produces from a
// reading this test does for itself — not merely checked for looking like DDL.
func TestTheDDLShownIsTheDDLTheGeneratorWrites(t *testing.T) {
	t.Parallel()

	tree, id, corpus, instance := realTree(t)

	session := testsupport.Session(t, instance)

	read, err := catalog.NewReader(session).Read(t.Context(), catalog.NewName(corpus))
	if err != nil {
		t.Fatalf("reading %s: %v", corpus, err)
	}

	for _, name := range []string{"Customer", "constrained", "parent"} {
		slice, found := read.Only(catalog.NewName(name))
		if !found {
			t.Fatalf("%s is not in the corpus", name)
		}

		want := ddl.Of(slice).String()

		got, err := tree.DDL(t.Context(), id, ui.ObjectRef{
			Database: testsupport.Database, Schema: corpus, Name: name,
		})
		if err != nil {
			t.Fatalf("asking for the DDL of %s: %v", name, err)
		}

		if got != want {
			t.Errorf("the panel shows a different script for %s:\n got %q\nwant %q", name, got, want)
		}

		if !strings.Contains(got, name) {
			t.Errorf("the script for %s does not name it: %q", name, got)
		}
	}
}

// The properties of a table are the columns the model read.
func TestThePropertiesOfATableAreItsColumns(t *testing.T) {
	t.Parallel()

	tree, id, corpus, _ := realTree(t)

	shown, err := tree.Properties(t.Context(), id, ui.ObjectRef{
		Database: testsupport.Database, Schema: corpus, Name: "column_shapes",
	})
	if err != nil {
		t.Fatalf("asking for the properties of column_shapes: %v", err)
	}

	if shown.Kind != "table" {
		t.Errorf("column_shapes came back as %q, want table", shown.Kind)
	}

	if len(shown.Columns) == 0 {
		t.Fatal("the table came back with no columns")
	}

	for _, column := range shown.Columns {
		if column.Name == "" || column.Type == "" {
			t.Errorf("a column came back as %+v, with nothing to print", column)
		}
	}
}

// A second object in the same schema is answered through the cache the first
// one filled.
//
// What it shows is that the second answer is right, not that it was cheap: the
// cache has its own tests where it lives, for reuse and for two callers at
// once, and a timing assertion here would be a flaky way of restating them.
// What only this level can show is that reading through the cache addresses an
// object by its name — the one chosen needs quoting, which is where a panel
// that had normalised the name on its way in would come apart.
func TestASecondObjectInTheSameSchemaIsAnsweredToo(t *testing.T) {
	t.Parallel()

	tree, id, corpus, _ := realTree(t)

	object := func(name string) ui.ObjectRef {
		return ui.ObjectRef{Database: testsupport.Database, Schema: corpus, Name: name}
	}

	if _, err := tree.Properties(t.Context(), id, object("parent")); err != nil {
		t.Fatalf("asking for the first object: %v", err)
	}

	// The corpus holds a table whose name needs quoting, so this also says the
	// cached model is addressed by the name and not by what it looks like.
	shown, err := tree.Properties(t.Context(), id, object("Customer"))
	if err != nil {
		t.Fatalf("asking for the second object: %v", err)
	}

	if shown.Name != "Customer" {
		t.Errorf("the second object came back as %q", shown.Name)
	}
}
