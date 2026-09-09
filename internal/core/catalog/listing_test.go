package catalog_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
	"github.com/gsoares85/hermes/internal/driver"
)

// asked stands in for a server answering a listing: it records every question
// and answers the rows the test set.
//
// It answers only Query, which is the whole point of it. A listing opens no
// transaction — see the doc on Lister — and a double that could open one would
// let a listing start doing so without a test noticing.
type asked struct {
	sql  []string
	args [][]any
	rows [][]any
	err  error
}

func (a *asked) Query(_ context.Context, sql string, args ...any) driver.Rows {
	a.sql = append(a.sql, sql)
	a.args = append(a.args, args)

	if a.err != nil {
		return &fakeRows{err: a.err}
	}

	return &fakeRows{rows: a.rows}
}

// Listing the objects of a schema is one question, whatever the schema holds.
//
// This is the decision the object tree is built on, and it is the one a later
// change is most likely to undo: reading a schema whole is eleven statements
// carrying every column, index and constraint, and expanding a node needs a
// name and a kind. Asking per object, or reaching for the whole read because it
// is already written, is what makes a tree take ten seconds to open a database
// with five thousand tables in it.
//
// Counted rather than timed, because against a container on the same machine a
// thousand round trips cost a fraction of a second and pass any clock.
func TestListingASchemaAsksOneQuestion(t *testing.T) {
	t.Parallel()

	server := &asked{rows: [][]any{{"orders", "r"}, {"open_orders", "v"}, {"orders_id_seq", "S"}}}

	if _, err := catalog.NewLister(server).Objects(t.Context(), catalog.NewName("sales"), ""); err != nil {
		t.Fatalf("listing sales: %v", err)
	}

	if len(server.sql) != 1 {
		t.Errorf("listing asked %d questions, want 1: %v", len(server.sql), server.sql)
	}
}

// The listing answers the kinds the model has, and nothing else.
//
// A partitioned table is a table: leaving relkind 'p' out would hide a table
// from the tree that the reader, the DDL writer and the diff all know about.
func TestTheListingAnswersTheKindsTheModelHas(t *testing.T) {
	t.Parallel()

	server := &asked{rows: [][]any{
		{"orders", "r"},
		{"events", "p"},
		{"open_orders", "v"},
		{"orders_id_seq", "S"},
	}}

	objects, err := catalog.NewLister(server).Objects(t.Context(), catalog.NewName("sales"), "")
	if err != nil {
		t.Fatalf("listing sales: %v", err)
	}

	want := []catalog.Object{
		{Kind: catalog.ObjectSequence, Name: catalog.NewName("orders_id_seq")},
		{Kind: catalog.ObjectTable, Name: catalog.NewName("events")},
		{Kind: catalog.ObjectTable, Name: catalog.NewName("orders")},
		{Kind: catalog.ObjectView, Name: catalog.NewName("open_orders")},
	}

	if !reflect.DeepEqual(objects, want) {
		t.Errorf("the listing answered %v, want %v", objects, want)
	}
}

// A relkind the query did not ask for is a fault, not a row to drop.
//
// The query names the kinds it wants, so anything else means the query and the
// answer disagree — and the same reasoning the reader applies to a column of a
// table it never listed applies here. Dropping the row would hide the mistake
// behind a tree that merely looks a little shorter than it should.
func TestAKindTheQueryDidNotAskForIsAFault(t *testing.T) {
	t.Parallel()

	server := &asked{rows: [][]any{{"orders", "r"}, {"summary", "m"}}}

	_, err := catalog.NewLister(server).Objects(t.Context(), catalog.NewName("sales"), "")
	if !errors.Is(err, catalog.ErrInconsistentCatalog) {
		t.Errorf("a relkind nothing asked for answered %v, want ErrInconsistentCatalog", err)
	}
}

// Listing the schemas is one question too.
func TestListingTheSchemasAsksOneQuestion(t *testing.T) {
	t.Parallel()

	server := &asked{rows: [][]any{{"public"}, {"sales"}}}

	schemas, err := catalog.NewLister(server).Schemas(t.Context(), catalog.Filter{})
	if err != nil {
		t.Fatalf("listing the schemas: %v", err)
	}

	if len(server.sql) != 1 {
		t.Errorf("listing asked %d questions, want 1: %v", len(server.sql), server.sql)
	}

	want := []catalog.Name{catalog.NewName("public"), catalog.NewName("sales")}
	if !reflect.DeepEqual(schemas, want) {
		t.Errorf("the listing answered %v, want %v", schemas, want)
	}
}

// The schemas PostgreSQL keeps for itself are left out unless they are asked
// for, and the two cases are two queries rather than one with a switch in it.
//
// Filtering them here instead would mean carrying every name across the wire to
// throw most of them away, which is the waste this whole listing exists to
// avoid — and the query that says what it selects is the one a person can read.
func TestTheSystemSchemasAreLeftOutUnlessTheyAreAskedFor(t *testing.T) {
	t.Parallel()

	hidden := &asked{}
	if _, err := catalog.NewLister(hidden).Schemas(t.Context(), catalog.Filter{}); err != nil {
		t.Fatalf("listing the schemas: %v", err)
	}

	shown := &asked{}
	if _, err := catalog.NewLister(shown).Schemas(t.Context(), catalog.Filter{System: true}); err != nil {
		t.Fatalf("listing every schema: %v", err)
	}

	if hidden.sql[0] == shown.sql[0] {
		t.Fatal("hiding the system schemas and showing them send the same query")
	}

	if !strings.Contains(hidden.sql[0], "pg\\_%") {
		t.Errorf("the default listing does not exclude the pg_ schemas: %s", hidden.sql[0])
	}

	if strings.Contains(shown.sql[0], "pg\\_%") {
		t.Errorf("the listing that was asked for everything still excludes some: %s", shown.sql[0])
	}
}

// The pattern goes to the server as an argument, and nothing filters again
// here.
//
// Two things at once, and both matter. It is an argument because it is typed by
// a person and a name is exactly the sort of thing that carries a quote. And
// the rows that come back are answered as they are: a second filter in Go would
// be a second definition of what matching means, and the two would disagree the
// first time one of them learned about case.
func TestThePatternGoesToTheServerAndIsNotAppliedTwice(t *testing.T) {
	t.Parallel()

	server := &asked{rows: [][]any{{"orders", "r"}, {"audit", "r"}}}

	objects, err := catalog.NewLister(server).Objects(t.Context(), catalog.NewName("sales"), "ord")
	if err != nil {
		t.Fatalf("listing sales: %v", err)
	}

	if strings.Contains(server.sql[0], "ord") {
		t.Errorf("the pattern was written into the query: %s", server.sql[0])
	}

	if !slicesContain(server.args[0], "ord") {
		t.Errorf("the query was sent with %v, want the pattern among the arguments", server.args[0])
	}

	if len(objects) != 2 {
		t.Errorf("the listing answered %d objects, want the 2 the server sent —"+
			" filtering here is a second definition of matching", len(objects))
	}
}

// The order is decided here rather than by the server.
//
// Ordering in SQL would order by the server's collation, which differs by
// version and by locale, and a tree that comes back in a different order
// against two servers holding the same schema is a tree nobody trusts. It is
// the same rule the model already sorts by.
func TestTheListingIsOrderedHereAndNotByTheServer(t *testing.T) {
	t.Parallel()

	server := &asked{rows: [][]any{{"zulu", "r"}, {"alpha", "r"}, {"mike", "v"}}}

	objects, err := catalog.NewLister(server).Objects(t.Context(), catalog.NewName("sales"), "")
	if err != nil {
		t.Fatalf("listing sales: %v", err)
	}

	if strings.Contains(strings.ToUpper(server.sql[0]), "ORDER BY") {
		t.Errorf("the listing asks the server to order: %s", server.sql[0])
	}

	want := []catalog.Object{
		{Kind: catalog.ObjectTable, Name: catalog.NewName("alpha")},
		{Kind: catalog.ObjectTable, Name: catalog.NewName("zulu")},
		{Kind: catalog.ObjectView, Name: catalog.NewName("mike")},
	}

	if !reflect.DeepEqual(objects, want) {
		t.Errorf("the listing answered %v, want %v", objects, want)
	}
}

// A schema this cannot name is answered like a schema that is not there.
//
// The same answer the reader gives, because it is the same question: the tree
// asked for something the server cannot hold, and "no such schema" is what
// sends a person to the right place.
func TestAnUnnameableSchemaIsNotFound(t *testing.T) {
	t.Parallel()

	server := &asked{}

	_, err := catalog.NewLister(server).Objects(t.Context(), catalog.NewName(""), "")
	if !errors.Is(err, catalog.ErrSchemaNotFound) {
		t.Errorf("listing a schema with no name answered %v, want ErrSchemaNotFound", err)
	}

	if len(server.sql) != 0 {
		t.Errorf("it asked the server anyway: %v", server.sql)
	}
}

// A failure says which schema was being listed.
func TestAFailedListingSaysWhatItWasListing(t *testing.T) {
	t.Parallel()

	server := &asked{err: errors.New("connection reset")}

	_, err := catalog.NewLister(server).Objects(t.Context(), catalog.NewName("sales"), "")
	if err == nil {
		t.Fatal("a server that failed answered no error")
	}

	if !strings.Contains(err.Error(), "sales") {
		t.Errorf("the failure reads %q, want the schema named in it", err)
	}
}

func slicesContain(values []any, want any) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}
