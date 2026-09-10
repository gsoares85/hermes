package catalog_test

import (
	"reflect"
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
)

// One object comes out as a schema of its own, so that the DDL of a table is
// written by the writer that writes the DDL of a schema.
//
// The alternative is a second generator for the panel that shows one table, and
// two writers of the same statements diverge: the day one learns about a
// storage parameter, the other is showing a definition that is quietly wrong.
func TestOnlyAnswersASchemaHoldingOneObject(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name: catalog.NewName("sales"),
		Tables: []catalog.Table{
			{Name: catalog.NewName("orders")},
			{Name: catalog.NewName("audit")},
		},
		Views: []catalog.View{{Name: catalog.NewName("open_orders")}},
	}

	slice, found := schema.Only(catalog.NewName("orders"))
	if !found {
		t.Fatal("orders is in the schema and Only did not find it")
	}

	if slice.Name != schema.Name {
		t.Errorf("the slice is of schema %s, want %s", slice.Name, schema.Name)
	}

	if len(slice.Tables) != 1 || slice.Tables[0].Name.String() != "orders" {
		t.Errorf("the slice holds the tables %v, want only orders", slice.Tables)
	}

	if len(slice.Views) != 0 {
		t.Errorf("the slice holds the views %v, want none", slice.Views)
	}
}

// A table brings the sequences it owns.
//
// A column that defaults to a sequence is defined in terms of it, and a slice
// without it reads as calling something that does not exist.
func TestOnlyBringsTheSequencesTheTableOwns(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name:   catalog.NewName("sales"),
		Tables: []catalog.Table{{Name: catalog.NewName("orders")}},
		Sequences: []catalog.Sequence{
			{Name: catalog.NewName("orders_id_seq"), OwnedBy: catalog.ColumnRef{
				Table: catalog.NewName("orders"), Column: catalog.NewName("id"),
			}},
			{Name: catalog.NewName("counter")},
		},
	}

	slice, found := schema.Only(catalog.NewName("orders"))
	if !found {
		t.Fatal("orders is in the schema and Only did not find it")
	}

	if len(slice.Sequences) != 1 || slice.Sequences[0].Name.String() != "orders_id_seq" {
		t.Errorf("the slice holds the sequences %v, want the one the table owns", slice.Sequences)
	}
}

// An edge is kept only when both ends are in the slice.
//
// It is the rule the ordering already applies to a model whose edges name
// something it does not hold, and applying it here rather than leaving a
// dangling edge is what keeps the slice a model the rest of the package can
// read.
func TestOnlyKeepsNoEdgeThatLeavesTheSlice(t *testing.T) {
	t.Parallel()

	orders := catalog.Object{Kind: catalog.ObjectTable, Name: catalog.NewName("orders")}
	view := catalog.Object{Kind: catalog.ObjectView, Name: catalog.NewName("open_orders")}

	schema := catalog.Schema{
		Name:         catalog.NewName("sales"),
		Tables:       []catalog.Table{{Name: catalog.NewName("orders")}},
		Views:        []catalog.View{{Name: catalog.NewName("open_orders")}},
		Dependencies: []catalog.Dependency{{Object: view, Needs: orders, Reason: catalog.ReasonQuery}},
	}

	slice, _ := schema.Only(catalog.NewName("orders"))
	if len(slice.Dependencies) != 0 {
		t.Errorf("the slice kept %v, and the other end of it is not there", slice.Dependencies)
	}
}

// An object the schema does not hold is not found, rather than an empty schema
// that looks like an object with nothing in it.
func TestOnlyDoesNotFindWhatIsNotThere(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name:   catalog.NewName("sales"),
		Tables: []catalog.Table{{Name: catalog.NewName("orders")}},
	}

	if slice, found := schema.Only(catalog.NewName("nothing")); found {
		t.Errorf("it found %v", slice)
	}
}

// The slice is a copy: editing it does not reach the schema it came from.
//
// What a cache answers is shared, and a caller that sliced one object out of it
// and then edited that slice would be editing what the next reader gets.
func TestTheSliceIsACopy(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name: catalog.NewName("sales"),
		Tables: []catalog.Table{{
			Name:    catalog.NewName("orders"),
			Columns: []catalog.Column{{Name: catalog.NewName("id"), Position: 1}},
		}},
	}

	slice, _ := schema.Only(catalog.NewName("orders"))
	slice.Tables[0].Columns[0].Name = catalog.NewName("edited")

	if got := schema.Tables[0].Columns[0].Name.String(); got != "id" {
		t.Errorf("editing the slice renamed the column of the original to %q", got)
	}
}

// The slice is ordered like any other model.
func TestTheSliceComesBackSorted(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name: catalog.NewName("sales"),
		Tables: []catalog.Table{{
			Name: catalog.NewName("orders"),
			Columns: []catalog.Column{
				{Name: catalog.NewName("total"), Position: 4},
				{Name: catalog.NewName("id"), Position: 1},
			},
		}},
	}

	slice, _ := schema.Only(catalog.NewName("orders"))

	want := []int{1, 2}
	got := []int{slice.Tables[0].Columns[0].Position, slice.Tables[0].Columns[1].Position}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("the columns are at %v, want %v — the slice was not sorted", got, want)
	}
}
