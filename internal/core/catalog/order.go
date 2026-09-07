package catalog

import (
	"slices"
	"strings"
)

// Sort puts the schema into the one order this model has.
//
// It is not tidiness. Go iterates a map in a different order on every run, and
// the reader assembles a schema out of maps — one query answers the tables,
// another the columns, another the constraints, and they are joined in memory.
// A model whose order came out of that assembly is a different model every time
// it is read, so it compares unequal to itself, and the diff reports changes
// nobody made. Making the order explicit here is what turns the reader's
// freedom to assemble however it likes into a value that is always the same.
//
// It is idempotent, and it reaches every level: a schema whose tables are
// ordered and whose constraints are not is still a model that compares unequal
// to itself.
func (s *Schema) Sort() {
	for i := range s.Tables {
		s.Tables[i].Sort()
	}

	byName(s.Tables, func(t Table) Name { return t.Name })
	byName(s.Sequences, func(q Sequence) Name { return q.Name })
	byName(s.Views, func(v View) Name { return v.Name })

	slices.SortStableFunc(s.Dependencies, compareDependencies)
}

// compareDependencies orders the edges of the graph.
//
// By all three fields, because all three are what an edge is: two objects can
// be related for more than one reason at once — a child table with a foreign
// key back to its parent — and ordering by less than the whole would leave two
// distinct edges in whichever order the assembly produced.
func compareDependencies(a, b Dependency) int {
	if order := compareObjects(a.Object, b.Object); order != 0 {
		return order
	}

	if order := compareObjects(a.Needs, b.Needs); order != 0 {
		return order
	}

	return strings.Compare(string(a.Reason), string(b.Reason))
}

// compareObjects orders two objects of a schema, by kind and then by name.
//
// The kind comes first so that a list of objects reads in groups rather than
// interleaved, and it is compared at all because it is part of what an object
// is here — the DDL writer needs to be told which kind of thing to create.
func compareObjects(a, b Object) int {
	if order := strings.Compare(string(a.Kind), string(b.Kind)); order != 0 {
		return order
	}

	return strings.Compare(a.Name.String(), b.Name.String())
}

// Sort orders everything defined on the table.
func (t *Table) Sort() {
	// Columns keep the order the table has rather than taking one of their
	// own: attnum is the order the server assigns, the order every tool shows,
	// and the order a CREATE TABLE has to reproduce. Sorting them by name would
	// put the model in an order no DDL can produce, and the round trip could
	// never close.
	slices.SortStableFunc(t.Columns, func(a, b Column) int { return a.Position - b.Position })

	byName(t.Constraints, func(c Constraint) Name { return c.Name })
	byName(t.Indexes, func(i Index) Name { return i.Name })
}

// byName orders a list of objects by the identifier that names them.
//
// The comparison is over bytes, not over the rules of a locale, and that is the
// point of having it in one place. Two machines reading the same schema have to
// produce the same model, and a locale-aware order puts an accented name in a
// different place depending on where the machine was bought — which the diff
// would read as objects having moved.
func byName[T any](objects []T, name func(T) Name) {
	slices.SortStableFunc(objects, func(a, b T) int {
		return strings.Compare(name(a).String(), name(b).String())
	})
}
