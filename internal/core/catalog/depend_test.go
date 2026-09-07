package catalog_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
)

// readDependencies answers what a reader makes of the pg_depend rows a server
// hands it, over a schema holding one table, one sequence and one view.
//
// The objects are fixed because none of these tests is about them: what each
// one is about is the edge, and an edge whose endpoints have to be declared
// again in every test is an edge nobody can read.
func readDependencies(t *testing.T, rows [][]any) catalog.Schema {
	t.Helper()

	server := &answers{rows: map[string][][]any{
		"pg_namespace":        existing(),
		"pg_class relkind IN": {{"orders", "r", false, "p", nil, nil}, {"customers", "r", false, "p", nil, nil}},
		"pg_class relkind =":  {{"open_orders", "SELECT 1", nil, nil}},
		"pg_sequence": {{"orders_id_seq", "integer",
			int64(1), int64(1), int64(1), int64(9), int64(1), false, nil, nil}},
		"pg_depend": rows,
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	return schema
}

// Every shape of dependency the model knows, read from the rows the catalog
// answers and turned into edges between objects of the model.
//
// The reason is carried rather than dropped, and that is what the DDL writer
// needs: a circle of foreign keys is broken by emitting the foreign keys
// afterwards, and a writer holding edges with no reason cannot tell which ones
// it is allowed to defer.
func TestDependenciesAreReadAsEdgesBetweenObjectsOfTheModel(t *testing.T) {
	t.Parallel()

	schema := readDependencies(t, [][]any{
		{"v", "open_orders", "query", "r", "orders"},
		{"r", "orders", "default", "S", "orders_id_seq"},
	})

	want := []catalog.Dependency{
		{Object: table("orders"), Needs: sequence("orders_id_seq"), Reason: catalog.ReasonDefault},
		{Object: view("open_orders"), Needs: table("orders"), Reason: catalog.ReasonQuery},
	}

	if !reflect.DeepEqual(schema.Dependencies, want) {
		t.Errorf("the dependencies read as %v, want %v", schema.Dependencies, want)
	}
}

// A partitioned table is a table, and so is a plain one. Both spellings of the
// relkind have to arrive as the same kind of object, or an edge between a
// partition and its parent would name two objects the schema does not hold.
func TestAPartitionedTableIsTheSameKindOfObjectAsAPlainOne(t *testing.T) {
	t.Parallel()

	schema := readDependencies(t, [][]any{{"p", "orders", "inheritance", "p", "customers"}})

	if len(schema.Dependencies) != 1 {
		t.Fatalf("the dependencies read as %v, want one", schema.Dependencies)
	}
	if got := schema.Dependencies[0].Object.Kind; got != catalog.ObjectTable {
		t.Errorf("a partitioned table reads as the object kind %q, want a table", got)
	}
}

// A row naming an object no query listed is the catalog answering two questions
// in ways that cannot both be true, and it is reported rather than dropped.
//
// Dropping it would leave the writer an order with a hole in it: the object it
// pointed at would be created in whatever position the model happened to hold,
// which is the one failure mode this whole file exists to prevent.
func TestADependencyOnSomethingThatWasNotListedIsInconsistent(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace":        existing(),
		"pg_class relkind IN": {{"orders", "r", false, "p", nil, nil}},
		"pg_depend":           {{"r", "orders", "foreign key", "r", "elsewhere"}},
	}}

	_, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if !errors.Is(err, catalog.ErrInconsistentCatalog) {
		t.Errorf("Read() = %v, want ErrInconsistentCatalog", err)
	}
}

// A failure reading the dependencies fails the read, like every other query.
//
// A schema answered without its edges is a schema whose order is a guess, and
// the writer has no way to tell that guess from an order that was worked out.
func TestAFailureReadingTheDependenciesFailsTheRead(t *testing.T) {
	t.Parallel()

	failure := errors.New("the connection went away")

	server := &answers{
		rows: map[string][][]any{"pg_namespace": existing(), "pg_class relkind IN": {{"orders", "r", false, "p", nil, nil}}},
		err:  map[string]error{"pg_depend": failure},
	}

	_, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if !errors.Is(err, failure) {
		t.Errorf("Read() = %v, want the failure the server reported", err)
	}
}

// A row the fixture got wrong is a failure of the read rather than a panic, so
// that a scan of the wrong width reads as the mistake it is.
func TestARowTheReaderCannotScanFailsTheRead(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace":        existing(),
		"pg_class relkind IN": {{"orders", "r", false, "p", nil, nil}},
		"pg_depend":           {{"r", "orders"}},
	}}

	if _, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales")); err == nil {
		t.Error("Read() accepted a row it could not scan")
	}
}

// The edges come out in one order whatever order the server listed them in,
// for the reason every other list in the model does: a model whose order came
// out of the assembly compares unequal to itself.
func TestDependenciesComeOutInOneOrder(t *testing.T) {
	t.Parallel()

	forwards := readDependencies(t, [][]any{
		{"v", "open_orders", "query", "r", "orders"},
		{"r", "orders", "default", "S", "orders_id_seq"},
	})

	backwards := readDependencies(t, [][]any{
		{"r", "orders", "default", "S", "orders_id_seq"},
		{"v", "open_orders", "query", "r", "orders"},
	})

	if !reflect.DeepEqual(forwards.Dependencies, backwards.Dependencies) {
		t.Errorf("two listings sort two ways:\n %v\n %v",
			forwards.Dependencies, backwards.Dependencies)
	}
}

// A reason this build does not know is carried through as it came, rather than
// dropped or renamed to something that reads as understood.
//
// It is the rule ADR-0007 states for objects and it holds for edges: a
// dependency from a version newer than this one is still an ordering
// constraint, and honouring it while admitting it is not understood beats
// either pretending or discarding.
func TestAReasonThisBuildDoesNotKnowIsCarriedThrough(t *testing.T) {
	t.Parallel()

	schema := readDependencies(t, [][]any{{"r", "orders", "something new", "S", "orders_id_seq"}})

	if len(schema.Dependencies) != 1 {
		t.Fatalf("the dependencies read as %v, want one", schema.Dependencies)
	}
	if got := schema.Dependencies[0].Reason; got != catalog.DependencyReason("something new") {
		t.Errorf("the reason reads as %q, want it carried through", got)
	}
}

// Two objects can be related for more than one reason at once — a child table
// with a foreign key back to its parent — and both edges survive.
//
// They also have to come out in one order, which is what the reason being part
// of the comparison buys: ordering by the two objects alone would leave the
// pair in whichever order the server listed them.
func TestTwoObjectsCanBeRelatedForMoreThanOneReason(t *testing.T) {
	t.Parallel()

	forwards := readDependencies(t, [][]any{
		{"r", "orders", "inheritance", "r", "customers"},
		{"r", "orders", "foreign key", "r", "customers"},
		{"r", "orders", "default", "S", "orders_id_seq"},
	})

	backwards := readDependencies(t, [][]any{
		{"r", "orders", "default", "S", "orders_id_seq"},
		{"r", "orders", "foreign key", "r", "customers"},
		{"r", "orders", "inheritance", "r", "customers"},
	})

	// Ordered by what is needed before why it is needed, and by the kind of
	// object before its name — so the sequence comes first and the two edges
	// to one table are told apart by their reason.
	want := []catalog.Dependency{
		{Object: table("orders"), Needs: sequence("orders_id_seq"), Reason: catalog.ReasonDefault},
		{Object: table("orders"), Needs: table("customers"), Reason: catalog.ReasonForeignKey},
		{Object: table("orders"), Needs: table("customers"), Reason: catalog.ReasonInheritance},
	}

	if !reflect.DeepEqual(forwards.Dependencies, want) {
		t.Errorf("the dependencies read as %v, want %v", forwards.Dependencies, want)
	}
	if !reflect.DeepEqual(backwards.Dependencies, want) {
		t.Errorf("the same edges listed backwards sort to %v", backwards.Dependencies)
	}
}

// A kind of object this build does not compare cannot become one it does.
//
// A materialised view is the case: it is a relation with storage of its own,
// the model does not hold it, and folding its relkind into "table" would put
// an object in the order that no CREATE TABLE can produce. The query filters
// them out, so this is the net under that — and it reports rather than guesses,
// because an object of an unknown kind is exactly the catalog saying something
// the model cannot represent.
func TestAKindOfObjectTheModelDoesNotHoldIsNotFoldedIntoOneItDoes(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace":        existing(),
		"pg_class relkind IN": {{"orders", "r", false, "p", nil, nil}},
		"pg_depend":           {{"m", "summary", "query", "r", "orders"}},
	}}

	_, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if !errors.Is(err, catalog.ErrInconsistentCatalog) {
		t.Errorf("Read() = %v, want ErrInconsistentCatalog", err)
	}
}
