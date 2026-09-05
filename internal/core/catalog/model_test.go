package catalog_test

import (
	"reflect"
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
)

// The property the whole package is built to hold, stated at the level this
// phase can already state it: a schema put together in one order and the same
// schema put together in another produce equal models.
//
// It is not a test about sorting. It is the diff's central promise —
// comparing a schema against itself finds nothing — reduced to the part that
// exists so far. Go iterates a map in a different order every run, so a reader
// that assembles from maps produces a different order each time; without this
// the round trip in a later phase would fail once in every so many runs, which
// is worse than failing always.
func TestASchemaSortsIntoOneOrderWhateverOrderItWasBuiltIn(t *testing.T) {
	t.Parallel()

	forwards := catalog.Schema{
		Name: catalog.NewName("sales"),
		Tables: []catalog.Table{
			{Name: catalog.NewName("orders"), Columns: []catalog.Column{
				{Name: catalog.NewName("id"), Position: 1, Type: catalog.NewTypeName("int4")},
				{Name: catalog.NewName("amount"), Position: 2, Type: catalog.NewTypeName("numeric(10,2)")},
			}},
			{Name: catalog.NewName("customers")},
		},
		Sequences: []catalog.Sequence{{Name: catalog.NewName("s2")}, {Name: catalog.NewName("s1")}},
		Views:     []catalog.View{{Name: catalog.NewName("v2")}, {Name: catalog.NewName("v1")}},
	}

	backwards := catalog.Schema{
		Name: catalog.NewName("sales"),
		Tables: []catalog.Table{
			{Name: catalog.NewName("customers")},
			{Name: catalog.NewName("orders"), Columns: []catalog.Column{
				{Name: catalog.NewName("amount"), Position: 2, Type: catalog.NewTypeName("decimal(10, 2)")},
				{Name: catalog.NewName("id"), Position: 1, Type: catalog.NewTypeName("integer")},
			}},
		},
		Sequences: []catalog.Sequence{{Name: catalog.NewName("s1")}, {Name: catalog.NewName("s2")}},
		Views:     []catalog.View{{Name: catalog.NewName("v1")}, {Name: catalog.NewName("v2")}},
	}

	forwards.Sort()
	backwards.Sort()

	if !reflect.DeepEqual(forwards, backwards) {
		t.Errorf("the same schema built in two orders is two models:\n %+v\n %+v", forwards, backwards)
	}
}

// Columns keep the order the table has, which is the order the server assigns
// and the order every tool shows. Sorting them by name would put a model in an
// order no DDL can reproduce, and the round trip would never close.
func TestColumnsKeepTheOrderOfTheTable(t *testing.T) {
	t.Parallel()

	table := catalog.Table{
		Name: catalog.NewName("orders"),
		Columns: []catalog.Column{
			{Name: catalog.NewName("amount"), Position: 3},
			{Name: catalog.NewName("id"), Position: 1},
			{Name: catalog.NewName("customer"), Position: 2},
		},
	}

	table.Sort()

	want := []string{"id", "customer", "amount"}
	for i, name := range want {
		if got := table.Columns[i].Name.String(); got != name {
			t.Errorf("column %d is %q, want %q", i, got, name)
		}
	}
}

// Everything that is not a column is ordered by name, and by bytes rather than
// by the rules of a locale: the same model read on two machines has to compare
// equal, and a locale-aware order would put accented names in a different place
// on each.
func TestObjectsAreOrderedByBytes(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{Tables: []catalog.Table{
		{Name: catalog.NewName("ação")},
		{Name: catalog.NewName("Zebra")},
		{Name: catalog.NewName("acao")},
		{Name: catalog.NewName("banana")},
	}}

	schema.Sort()

	// Upper case sorts before lower case, and a multi-byte character after
	// every ASCII one. That is byte order, and it is what both machines agree
	// on.
	want := []string{"Zebra", "acao", "ação", "banana"}
	for i, name := range want {
		if got := schema.Tables[i].Name.String(); got != name {
			t.Errorf("table %d is %q, want %q", i, got, name)
		}
	}
}

// Sorting reaches every level. A schema whose tables are ordered but whose
// constraints are not is a model that still compares unequal to itself.
func TestSortingReachesInsideEveryTable(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{Tables: []catalog.Table{{
		Name: catalog.NewName("orders"),
		Constraints: []catalog.Constraint{
			{Name: catalog.NewName("c2"), Kind: catalog.ConstraintCheck},
			{Name: catalog.NewName("c1"), Kind: catalog.ConstraintPrimaryKey},
		},
		Indexes: []catalog.Index{
			{Name: catalog.NewName("i2")},
			{Name: catalog.NewName("i1")},
		},
	}}}

	schema.Sort()

	table := schema.Tables[0]
	if table.Constraints[0].Name.String() != "c1" {
		t.Errorf("constraints are %v, want them ordered", table.Constraints)
	}
	if table.Indexes[0].Name.String() != "i1" {
		t.Errorf("indexes are %v, want them ordered", table.Indexes)
	}
}

// Sorting the same schema twice changes nothing the second time. A sort that
// is not idempotent is a model whose order depends on how many times it was
// read.
func TestSortingTwiceChangesNothing(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name:   catalog.NewName("sales"),
		Tables: []catalog.Table{{Name: catalog.NewName("b")}, {Name: catalog.NewName("a")}},
	}

	twice := catalog.Schema{
		Name:   catalog.NewName("sales"),
		Tables: []catalog.Table{{Name: catalog.NewName("b")}, {Name: catalog.NewName("a")}},
	}

	schema.Sort()

	twice.Sort()
	twice.Sort()

	if !reflect.DeepEqual(schema, twice) {
		t.Errorf("sorting twice is not sorting once:\n once %+v\ntwice %+v", schema, twice)
	}
}

// An empty schema sorts without complaint. It is what a reader answers for a
// schema somebody created and has not used yet, and a panic there would be a
// crash on the most ordinary thing in the product.
func TestAnEmptySchemaSorts(t *testing.T) {
	t.Parallel()

	var empty catalog.Schema
	empty.Sort()
}
