package ui

import (
	"reflect"
	"slices"
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
)

// The panel prints text, and this is where the model becomes it. The tests are
// internal because that translation is not a boundary anybody calls — it is the
// inside of two methods that need a server to reach.

// A table becomes its columns, with what is declared on it beside them.
func TestATableBecomesItsColumns(t *testing.T) {
	t.Parallel()

	slice := catalog.Schema{
		Name: catalog.NewName("sales"),
		Tables: []catalog.Table{{
			Name: catalog.NewName("orders"),
			Columns: []catalog.Column{
				{
					Name: catalog.NewName("id"), Position: 1,
					Type: catalog.NewTypeName("integer"), NotNull: true, Identity: "BY DEFAULT",
				},
				{
					Name: catalog.NewName("total"), Position: 2,
					Type: catalog.NewTypeName("numeric(12,2)"), Default: "0",
				},
			},
			Constraints: []catalog.Constraint{{
				Name: catalog.NewName("orders_pkey"), Definition: "PRIMARY KEY (id)",
			}},
			Indexes: []catalog.Index{{
				Name: catalog.NewName("orders_total_idx"), Definition: "CREATE INDEX orders_total_idx ON orders (total)",
			}},
		}},
	}

	shown := propertiesOf(slice, "orders")

	if shown.Kind != string(catalog.ObjectTable) || shown.Name != "orders" {
		t.Fatalf("the table came back as %q %q", shown.Kind, shown.Name)
	}

	want := []ColumnView{
		{Name: "id", Type: "integer", NotNull: true, Identity: "BY DEFAULT"},
		{Name: "total", Type: "numeric(12,2)", Default: "0"},
	}

	if !reflect.DeepEqual(shown.Columns, want) {
		t.Errorf("the columns are %+v, want %+v", shown.Columns, want)
	}

	if len(shown.Constraints) != 1 || !slices.Contains(shown.Constraints, "orders_pkey: PRIMARY KEY (id)") {
		t.Errorf("the constraints are %v", shown.Constraints)
	}

	if len(shown.Indexes) != 1 {
		t.Errorf("the indexes are %v", shown.Indexes)
	}
}

// The notes are the facts a copy would behave differently without, which is the
// same reason the model reads them at all.
func TestTheNotesSayWhatIsNotAColumn(t *testing.T) {
	t.Parallel()

	table := catalog.Table{
		Name:        catalog.NewName("measurements"),
		Unlogged:    true,
		Partitioned: true,
		RowSecurity: true,
		Forced:      true,
		Inherits:    []catalog.Name{catalog.NewName("parent")},
		Options:     []string{"fillfactor=70"},
	}

	notes := tableNotes(table)

	for _, want := range []string{
		"unlogged", "partitioned", "row security enabled", "row security forced",
		"inherits parent", "fillfactor=70",
	} {
		if !slices.Contains(notes, want) {
			t.Errorf("the notes %v do not say %q", notes, want)
		}
	}
}

// A table with nothing unusual about it has nothing to note, rather than a list
// of the things it is not.
func TestAnOrdinaryTableHasNoNotes(t *testing.T) {
	t.Parallel()

	if notes := tableNotes(catalog.Table{Name: catalog.NewName("orders")}); len(notes) != 0 {
		t.Errorf("an ordinary table notes %v", notes)
	}
}

// A view carries its definition, which is the only thing there is to show about
// one.
func TestAViewCarriesItsDefinition(t *testing.T) {
	t.Parallel()

	slice := catalog.Schema{
		Views: []catalog.View{{
			Name:        catalog.NewName("open_orders"),
			Definition:  "SELECT id FROM orders",
			CheckOption: "cascaded",
		}},
	}

	shown := propertiesOf(slice, "open_orders")

	if shown.Kind != string(catalog.ObjectView) {
		t.Errorf("the view came back as %q", shown.Kind)
	}

	if !slices.Contains(shown.Notes, "SELECT id FROM orders") {
		t.Errorf("the notes %v do not carry the definition", shown.Notes)
	}

	if !slices.Contains(shown.Notes, "check option: cascaded") {
		t.Errorf("the notes %v do not carry the check option, which decides whether"+
			" the view accepts writes the original refuses", shown.Notes)
	}
}

// A sequence carries every parameter, because a copy made with the defaults
// hands out numbers the original never would.
func TestASequenceCarriesItsParameters(t *testing.T) {
	t.Parallel()

	slice := catalog.Schema{
		Sequences: []catalog.Sequence{{
			Name:      catalog.NewName("orders_id_seq"),
			Type:      catalog.NewTypeName("bigint"),
			Start:     100,
			Increment: 5,
			Min:       1,
			Max:       999,
			Cycle:     true,
			OwnedBy: catalog.ColumnRef{
				Table: catalog.NewName("orders"), Column: catalog.NewName("id"),
			},
		}},
	}

	shown := propertiesOf(slice, "orders_id_seq")

	if shown.Kind != string(catalog.ObjectSequence) {
		t.Errorf("the sequence came back as %q", shown.Kind)
	}

	for _, want := range []string{
		"bigint starting at 100, by 5", "between 1 and 999", "cycles", "owned by orders.id",
	} {
		if !slices.Contains(shown.Notes, want) {
			t.Errorf("the notes %v do not say %q", shown.Notes, want)
		}
	}
}

// An object the slice does not hold comes back named and empty rather than as a
// table with nothing in it.
func TestAnObjectTheSliceDoesNotHoldIsEmpty(t *testing.T) {
	t.Parallel()

	shown := propertiesOf(catalog.Schema{}, "gone")

	if shown.Name != "gone" || shown.Kind != "" {
		t.Errorf("it came back as %q %q", shown.Kind, shown.Name)
	}
}

// The caches of a connection that closed are dropped, and the ones beside them
// are not.
func TestForgettingDropsOnlyThatConnection(t *testing.T) {
	t.Parallel()

	service := &CatalogService{caches: map[string]*catalog.Cache{
		cacheKey("one", "app"):      nil,
		cacheKey("one", "billing"):  nil,
		cacheKey("another", "app"):  nil,
		cacheKey("oneother", "app"): nil,
	}}

	service.forget("one")

	left := make([]string, 0, len(service.caches))
	for key := range service.caches {
		left = append(left, key)
	}

	slices.Sort(left)

	want := []string{cacheKey("another", "app"), cacheKey("oneother", "app")}
	slices.Sort(want)

	if !reflect.DeepEqual(left, want) {
		t.Errorf("what is left is %q, want %q", left, want)
	}
}
