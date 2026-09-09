//go:build integration

package catalog_test

import (
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
	"github.com/gsoares85/hermes/internal/testsupport"
)

// orderOfCorpus reads the pathological schema off a real server and works out
// what has to be created before what.
func orderOfCorpus(t *testing.T, version string) (catalog.Schema, catalog.Order) {
	t.Helper()

	schema := readCorpus(t, version)

	return schema, schema.Order()
}

// dependsOn reports whether the schema holds an edge between two objects, for
// whichever reason.
func dependsOn(schema catalog.Schema, object, needed catalog.Object) bool {
	for _, edge := range schema.Dependencies {
		if edge.Object == object && edge.Needs == needed {
			return true
		}
	}

	return false
}

// The property the phase exists for, checked against every edge a real server
// reported rather than against the handful the tests name.
//
// Every object comes after everything it needs, unless the two are in a cycle
// together — which is the one case no order can satisfy and the reason cycles
// are reported instead. A single assertion over the whole corpus is what makes
// this hold for the shapes nobody thought to write a test for.
func TestEveryObjectOfTheCorpusIsOrderedAfterWhatItNeeds(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			schema, order := orderOfCorpus(t, version)

			for _, edge := range schema.Dependencies {
				if inTheSameCycle(order, edge.Object, edge.Needs) {
					continue
				}

				mustPrecede(t, order, edge.Needs, edge.Object)
			}
		})
	}
}

func inTheSameCycle(order catalog.Order, first, second catalog.Object) bool {
	for _, cycle := range order.Cycles {
		found := 0

		for _, member := range cycle {
			if member == first || member == second {
				found++
			}
		}

		if found == 2 {
			return true
		}
	}

	return false
}

// The order holds the whole schema, not only the part of it that has edges.
//
// The DDL is written from this list, so an object missing from it is an object
// missing from the script — and the failure would be a target that is quietly
// incomplete rather than one that reports anything.
func TestTheOrderHoldsEveryObjectOfTheCorpus(t *testing.T) {
	t.Parallel()

	schema, order := orderOfCorpus(t, testsupport.SupportedVersions[0])

	want := len(schema.Tables) + len(schema.Sequences) + len(schema.Views)
	if len(order.Objects) != want {
		t.Errorf("the order holds %d objects and the schema %d", len(order.Objects), want)
	}
}

// Foreign keys in a circle, off a real server: legal, unorderable, and reported
// as the cycle it is rather than as a failure or as an order that happens to be
// wrong.
func TestTheCircularForeignKeysOfTheCorpusComeBackAsACycle(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			_, order := orderOfCorpus(t, version)

			if !inTheSameCycle(order, table("circle_a"), table("circle_b")) {
				t.Errorf("the circular foreign keys read as the cycles %v, want the two tables in one",
					order.Cycles)
			}
		})
	}
}

// A table whose foreign key points at itself is not a cycle, and neither is a
// view whose query refers to itself through a recursive CTE.
//
// Both are legal, both resolve where they stand, and both are shapes a reader
// that took pg_depend at face value would report as unorderable — which would
// make the writer defer a foreign key that CREATE TABLE was perfectly able to
// carry, on the ordinary shape of a table with a manager column.
func TestSomethingThatRefersToItselfIsNotACycle(t *testing.T) {
	t.Parallel()

	schema, order := orderOfCorpus(t, testsupport.SupportedVersions[0])

	for _, object := range []catalog.Object{table("employee"), view("reporting_line")} {
		if dependsOn(schema, object, object) {
			t.Errorf("%s reads as needing itself", object.Name)
		}

		for _, cycle := range order.Cycles {
			for _, member := range cycle {
				if member == object {
					t.Errorf("%s reads as part of the cycle %v", object.Name, cycle)
				}
			}
		}
	}
}

// A child comes after the table it inherits from, and a partition after the
// table it partitions.
//
// Two shapes and one edge: PostgreSQL records both the same way, which is why
// the reader does not have to tell them apart and why leaving either out would
// have left the other broken too.
func TestInheritanceAndPartitioningAreOrderedAfterTheParent(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			schema, order := orderOfCorpus(t, version)

			for child, parent := range map[string]string{
				"child":             "parent",
				"measurements_2026": "measurements",
			} {
				if !dependsOn(schema, table(child), table(parent)) {
					t.Errorf("%s does not read as inheriting %s", child, parent)
				}

				mustPrecede(t, order, table(parent), table(child))
			}
		})
	}
}

// A view comes after what it selects from, and a view over a view after that
// one — which is the chain the whole ordering exists to get right, because
// creating a view over something that is not there yet fails outright.
func TestAViewIsOrderedAfterWhatItSelectsFrom(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			schema, order := orderOfCorpus(t, version)

			if !dependsOn(schema, view("active_orders"), table("indexed")) {
				t.Error("the view does not read as needing the table it selects from")
			}
			if !dependsOn(schema, view("active_domains"), view("active_orders")) {
				t.Error("the view over a view does not read as needing it")
			}

			mustPrecede(t, order, table("indexed"), view("active_orders"))
			mustPrecede(t, order, view("active_orders"), view("active_domains"))
		})
	}
}

// A table comes after the sequence its default calls, which is what puts
// CREATE SEQUENCE in front of CREATE TABLE for every serial column there is.
//
// And the ownership is not read the other way round. A sequence a column owns
// carries a dependency on that column, and reading it as an edge would put the
// sequence after the table whose default calls it — a cycle in every schema
// that has a serial column, and a false one, because OWNED BY is written
// afterwards in a statement of its own.
func TestATableIsOrderedAfterTheSequenceItsDefaultCalls(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			schema, order := orderOfCorpus(t, version)

			for owner, counter := range map[string]string{
				"counted":      "counted_id_seq",
				"type_aliases": "type_aliases_o_seq",
			} {
				if !dependsOn(schema, table(owner), sequence(counter)) {
					t.Errorf("%s does not read as needing %s", owner, counter)
				}
				if dependsOn(schema, sequence(counter), table(owner)) {
					t.Errorf("%s reads as needing %s, which is the ownership and not an order",
						counter, owner)
				}

				mustPrecede(t, order, sequence(counter), table(owner))
			}
		})
	}
}

// The sequence behind an identity column is not ordered against its table at
// all, in either direction.
//
// Nobody declared it and nothing creates it separately: the column's own clause
// does. An edge either way would be an ordering constraint on a statement that
// is never written.
func TestTheSequenceBehindAnIdentityColumnIsNotAnEdge(t *testing.T) {
	t.Parallel()

	schema, _ := orderOfCorpus(t, testsupport.SupportedVersions[0])

	identity := sequence("column_shapes_id_seq")
	owner := table("column_shapes")

	if dependsOn(schema, owner, identity) || dependsOn(schema, identity, owner) {
		t.Errorf("the sequence behind an identity column reads as an edge: %v", schema.Dependencies)
	}
}

// A dependency on something outside the schema is left out, and leaving it out
// is not the reader deciding the catalog contradicted itself.
//
// The model is of one schema and an order cannot place an object it does not
// hold. A view over pg_catalog is the cheapest way to hold that shape in the
// corpus, and the property it proves is the one a real database breaks first:
// a schema with a foot in another one still reads.
func TestADependencyOnAnotherSchemaIsNotAnEdge(t *testing.T) {
	t.Parallel()

	schema, order := orderOfCorpus(t, testsupport.SupportedVersions[0])

	peek := view("catalog_peek")

	for _, edge := range schema.Dependencies {
		if edge.Object == peek {
			t.Errorf("the view over another schema reads as needing %s", edge.Needs.Name)
		}
	}

	if positionOf(order, peek) < 0 {
		t.Error("the view over another schema is not in the order at all")
	}
}
