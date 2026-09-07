package catalog_test

import (
	"reflect"
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
)

// The objects the ordering tests are written in terms of. Building one by hand
// is three lines of struct literal, and a test about order should read as an
// order rather than as the model underneath it.
func table(name string) catalog.Object {
	return catalog.Object{Kind: catalog.ObjectTable, Name: catalog.NewName(name)}
}

func view(name string) catalog.Object {
	return catalog.Object{Kind: catalog.ObjectView, Name: catalog.NewName(name)}
}

func sequence(name string) catalog.Object {
	return catalog.Object{Kind: catalog.ObjectSequence, Name: catalog.NewName(name)}
}

// positionOf answers where an object came out, and -1 when it did not.
func positionOf(order catalog.Order, object catalog.Object) int {
	for i, ordered := range order.Objects {
		if ordered == object {
			return i
		}
	}

	return -1
}

// mustPrecede fails unless the first object is created before the second.
func mustPrecede(t *testing.T, order catalog.Order, first, second catalog.Object) {
	t.Helper()

	before, after := positionOf(order, first), positionOf(order, second)

	switch {
	case before < 0:
		t.Errorf("%s is not in the order at all; it holds %v", first.Name, order.Objects)
	case after < 0:
		t.Errorf("%s is not in the order at all; it holds %v", second.Name, order.Objects)
	case before > after:
		t.Errorf("%s comes after %s, and it needs it to exist first", second.Name, first.Name)
	}
}

// The property the whole file is about: nothing is created before what it
// needs.
//
// The chain here is the ordinary shape of a schema — a view over a table whose
// default calls a sequence — and it is the shape that breaks first when the
// order is left to whatever the reader assembled. Running the DDL in the wrong
// order does not produce a subtly different database; it produces a failed
// statement halfway through, on a target that is now half migrated.
func TestAnObjectIsOrderedAfterWhatItNeeds(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name:      catalog.NewName("sales"),
		Tables:    []catalog.Table{{Name: catalog.NewName("orders")}},
		Sequences: []catalog.Sequence{{Name: catalog.NewName("orders_id_seq")}},
		Views:     []catalog.View{{Name: catalog.NewName("open_orders")}},
		Dependencies: []catalog.Dependency{
			{Object: view("open_orders"), Needs: table("orders"), Reason: catalog.ReasonQuery},
			{Object: table("orders"), Needs: sequence("orders_id_seq"), Reason: catalog.ReasonDefault},
		},
	}

	order := schema.Order()

	mustPrecede(t, order, sequence("orders_id_seq"), table("orders"))
	mustPrecede(t, order, table("orders"), view("open_orders"))

	if len(order.Cycles) != 0 {
		t.Errorf("a chain reports the cycles %v", order.Cycles)
	}
}

// An object nothing points at is still an object, and leaving it out would
// leave it out of the DDL as well.
func TestAnObjectWithNothingToDependOnIsStillOrdered(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name:      catalog.NewName("sales"),
		Tables:    []catalog.Table{{Name: catalog.NewName("orders")}, {Name: catalog.NewName("audit")}},
		Sequences: []catalog.Sequence{{Name: catalog.NewName("counter")}},
		Views:     []catalog.View{{Name: catalog.NewName("recent")}},
	}

	order := schema.Order()

	if len(order.Objects) != 4 {
		t.Errorf("the order holds %v, want every object of the schema", order.Objects)
	}
}

// A circle of foreign keys is legal in PostgreSQL, no CREATE TABLE order
// produces it, and a sort that answered an error here would refuse to describe
// a schema the server is perfectly happy to hold.
//
// So a cycle is a result. The objects in it are still ordered — the writer has
// to create them — and the cycle is named beside the order so the writer knows
// which edges it has to break by adding the foreign keys afterwards, in
// ALTER TABLE statements of their own. That is what pg_dump does and the only
// thing that works.
func TestACircleComesBackAsACycleRatherThanAsAFailure(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name:   catalog.NewName("sales"),
		Tables: []catalog.Table{{Name: catalog.NewName("a")}, {Name: catalog.NewName("b")}},
		Dependencies: []catalog.Dependency{
			{Object: table("a"), Needs: table("b"), Reason: catalog.ReasonForeignKey},
			{Object: table("b"), Needs: table("a"), Reason: catalog.ReasonForeignKey},
		},
	}

	order := schema.Order()

	if len(order.Objects) != 2 {
		t.Errorf("the order holds %v, want both tables", order.Objects)
	}

	want := [][]catalog.Object{{table("a"), table("b")}}
	if !reflect.DeepEqual(order.Cycles, want) {
		t.Errorf("the cycles read as %v, want %v", order.Cycles, want)
	}
}

// The circle is reported once, as one group, rather than once per member.
//
// Three tables in a ring are one thing that cannot be ordered, and a writer
// told about it three times would break three edges where one is enough.
func TestOneCircleIsOneCycleWhateverItsSize(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name: catalog.NewName("sales"),
		Tables: []catalog.Table{
			{Name: catalog.NewName("a")}, {Name: catalog.NewName("b")}, {Name: catalog.NewName("c")},
		},
		Dependencies: []catalog.Dependency{
			{Object: table("a"), Needs: table("b"), Reason: catalog.ReasonForeignKey},
			{Object: table("b"), Needs: table("c"), Reason: catalog.ReasonForeignKey},
			{Object: table("c"), Needs: table("a"), Reason: catalog.ReasonForeignKey},
		},
	}

	if cycles := schema.Order().Cycles; len(cycles) != 1 || len(cycles[0]) != 3 {
		t.Errorf("three tables in a ring read as the cycles %v, want one of three", cycles)
	}
}

// Everything outside the circle still gets a real order.
//
// A sort that gave up on the whole schema the moment it found one cycle would
// hand the writer a list in no order at all, and the ninety-nine tables that
// have nothing to do with the circle would be created in whatever order the
// model happened to hold.
func TestACircleDoesNotCostTheRestOfTheSchemaItsOrder(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name: catalog.NewName("sales"),
		Tables: []catalog.Table{
			{Name: catalog.NewName("a")}, {Name: catalog.NewName("b")}, {Name: catalog.NewName("parent")},
		},
		Views: []catalog.View{{Name: catalog.NewName("over_a")}},
		Dependencies: []catalog.Dependency{
			{Object: table("a"), Needs: table("b"), Reason: catalog.ReasonForeignKey},
			{Object: table("b"), Needs: table("a"), Reason: catalog.ReasonForeignKey},
			{Object: table("a"), Needs: table("parent"), Reason: catalog.ReasonInheritance},
			{Object: view("over_a"), Needs: table("a"), Reason: catalog.ReasonQuery},
		},
	}

	order := schema.Order()

	mustPrecede(t, order, table("parent"), table("a"))
	mustPrecede(t, order, table("a"), view("over_a"))
}

// The order is a value the DDL is written from, so it has to be the same value
// every time the same schema is read.
//
// Go iterates a map in a different order on every run, and a sort that walked
// one would answer a different order per run — which turns the generated script
// into a file that changes when nothing changed, and the round trip into a test
// that fails once in every so many runs.
func TestTheOrderIsTheSameWhateverOrderTheModelWasBuiltIn(t *testing.T) {
	t.Parallel()

	forwards := catalog.Schema{
		Name: catalog.NewName("sales"),
		Tables: []catalog.Table{
			{Name: catalog.NewName("a")}, {Name: catalog.NewName("b")}, {Name: catalog.NewName("c")},
		},
		Views: []catalog.View{{Name: catalog.NewName("v")}, {Name: catalog.NewName("w")}},
		Dependencies: []catalog.Dependency{
			{Object: view("v"), Needs: table("a"), Reason: catalog.ReasonQuery},
			{Object: view("w"), Needs: table("a"), Reason: catalog.ReasonQuery},
			{Object: table("b"), Needs: table("c"), Reason: catalog.ReasonForeignKey},
			{Object: table("c"), Needs: table("b"), Reason: catalog.ReasonForeignKey},
		},
	}

	backwards := catalog.Schema{
		Name: catalog.NewName("sales"),
		Tables: []catalog.Table{
			{Name: catalog.NewName("c")}, {Name: catalog.NewName("b")}, {Name: catalog.NewName("a")},
		},
		Views: []catalog.View{{Name: catalog.NewName("w")}, {Name: catalog.NewName("v")}},
		Dependencies: []catalog.Dependency{
			{Object: table("c"), Needs: table("b"), Reason: catalog.ReasonForeignKey},
			{Object: table("b"), Needs: table("c"), Reason: catalog.ReasonForeignKey},
			{Object: view("w"), Needs: table("a"), Reason: catalog.ReasonQuery},
			{Object: view("v"), Needs: table("a"), Reason: catalog.ReasonQuery},
		},
	}

	forwards.Sort()
	backwards.Sort()

	if !reflect.DeepEqual(forwards.Order(), backwards.Order()) {
		t.Errorf("the same schema built in two orders sorts two ways:\n %v\n %v",
			forwards.Order(), backwards.Order())
	}
}

// An edge to something the schema does not hold is not an order this schema can
// express.
//
// The reader refuses such a model outright, so this guards the one that was
// built by hand — a diff comparing a hand-made target, a test fixture. Ordering
// around a phantom would put an object nobody can create into the list the
// writer emits from.
func TestAnEdgeToAnObjectTheSchemaDoesNotHoldIsNotAnOrder(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name:   catalog.NewName("sales"),
		Tables: []catalog.Table{{Name: catalog.NewName("orders")}},
		Dependencies: []catalog.Dependency{
			{Object: table("orders"), Needs: table("elsewhere"), Reason: catalog.ReasonForeignKey},
		},
	}

	order := schema.Order()

	want := []catalog.Object{table("orders")}
	if !reflect.DeepEqual(order.Objects, want) {
		t.Errorf("the order reads as %v, want only the objects the schema holds", order.Objects)
	}
}
