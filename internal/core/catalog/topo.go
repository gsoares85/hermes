package catalog

import "slices"

// Order is every object of a schema in an order that creates each one after
// what it needs, together with the cycles no order can satisfy.
//
// Both fields, and that is the decision this file records. A circle of foreign
// keys is legal in PostgreSQL, no CREATE TABLE order produces it, and a sort
// that answered a failure would refuse to describe a schema the server is
// perfectly happy to hold. So the cycle is a result: the objects in it are
// still ordered, because the writer has to create them, and the cycle is named
// beside the order so the writer knows which relationships it has to add
// afterwards in statements of their own. That is what pg_dump does with foreign
// keys and the only thing that works.
type Order struct {
	// Objects is every object of the schema, each after everything it needs
	// that is not in a cycle with it.
	Objects []Object

	// Cycles are the groups of objects that need each other, in the order they
	// appear in Objects. A group is here only when it has more than one member:
	// an object never needs itself, so a single object is never a cycle.
	Cycles [][]Object
}

// Order works out what has to be created before what.
//
// It is Tarjan's algorithm, chosen because it answers both halves of the
// question in one pass: the strongly connected components of a graph are its
// cycles, and they come out in an order where every component is emitted after
// the ones it depends on. A plain topological sort would answer the order and
// then have to be told separately where it gave up.
//
// The result is the same for the same model, whatever order the model was
// assembled in. That is not a courtesy either: the DDL is written from this
// order, and an order that varied per run would turn a generated script into a
// file that changes when nothing changed. Sort is what the model relies on for
// it — the nodes are walked as the schema holds them and the edges as Sort left
// them, so nothing here reads a map to decide an order.
func (s Schema) Order() Order {
	sorter := newSorter(s)

	for _, object := range sorter.objects {
		if !sorter.seen(object) {
			sorter.visit(object)
		}
	}

	return sorter.order
}

// sorter carries the state of one run of Tarjan's algorithm.
type sorter struct {
	// objects is every node, in the order the schema holds them, and needs is
	// the adjacency built from the edges the schema holds. An edge naming an
	// object the schema does not hold is left out — the reader refuses such a
	// model, so what this guards is one built by hand, and ordering around a
	// phantom would put an object nobody can create into the list the writer
	// emits from.
	objects []Object
	needs   map[Object][]Object

	// index is the order a node was reached in, low the earliest index
	// reachable from it, and the two being equal is what makes a node the root
	// of a component. stack holds the nodes of components not yet closed.
	index   map[Object]int
	low     map[Object]int
	stacked map[Object]bool
	stack   []Object
	reached int

	order Order
}

func newSorter(schema Schema) *sorter {
	sorter := &sorter{
		objects: objectsIn(schema),
		needs:   map[Object][]Object{},
		index:   map[Object]int{},
		low:     map[Object]int{},
		stacked: map[Object]bool{},
	}

	known := objectsOf(schema)

	for _, edge := range schema.Dependencies {
		if known[edge.Object] && known[edge.Needs] {
			sorter.needs[edge.Object] = append(sorter.needs[edge.Object], edge.Needs)
		}
	}

	return sorter
}

// objectsIn is every object of the schema, in the order the model holds them.
//
// Sequences before tables and tables before views, which is not the order the
// result comes out in — the edges decide that — but is the order two objects
// with nothing between them are left in. It is the order a schema with no
// dependencies at all reads best in, and it costs nothing to prefer it.
func objectsIn(schema Schema) []Object {
	objects := make([]Object, 0, len(schema.Sequences)+len(schema.Tables)+len(schema.Views))

	for _, sequence := range schema.Sequences {
		objects = append(objects, Object{Kind: ObjectSequence, Name: sequence.Name})
	}

	for _, table := range schema.Tables {
		objects = append(objects, Object{Kind: ObjectTable, Name: table.Name})
	}

	for _, view := range schema.Views {
		objects = append(objects, Object{Kind: ObjectView, Name: view.Name})
	}

	return objects
}

func (s *sorter) seen(object Object) bool {
	_, reached := s.index[object]

	return reached
}

// step is one object part way through being walked: which of the things it
// needs have been looked at, and which object the walk arrived from.
//
// It is the state a recursive version would have kept on the goroutine stack.
type step struct {
	object Object
	from   Object
	next   int
	rooted bool
}

// visit walks everything reachable from an object and closes the components it
// roots.
//
// Iterative, over a stack this function owns, and that is not a matter of
// taste. The depth of the walk is the length of the longest chain of
// dependencies, which comes from a database: a thousand tables each with a
// foreign key to the one before is an ordinary shape and the performance
// fixture builds exactly it. A goroutine stack grows to a gigabyte and would
// survive that, but the failure when it does not is fatal — a stack overflow
// cannot be recovered, so a schema deep enough would take the whole application
// down rather than the one operation, which is the opposite of what the product
// promises about long operations. An explicit stack grows on the heap and fails
// like anything else that runs out of memory.
func (s *sorter) visit(root Object) {
	s.open(root)

	walk := []step{{object: root, rooted: true}}

	for len(walk) > 0 {
		top := &walk[len(walk)-1]

		if top.next < len(s.needs[top.object]) {
			needed := s.needs[top.object][top.next]
			top.next++

			switch {
			case !s.seen(needed):
				s.open(needed)
				walk = append(walk, step{object: needed, from: top.object})
			case s.stacked[needed]:
				// Reached again while still open, which is a way back to where
				// the walk came from — the definition of a cycle.
				s.low[top.object] = min(s.low[top.object], s.index[needed])
			}

			continue
		}

		// Everything this object needs has been walked, which is where the
		// recursive version returned: the component is closed if this object
		// roots one, and what it learned goes back to whoever sent the walk
		// here.
		if s.low[top.object] == s.index[top.object] {
			s.close(top.object)
		}

		if !top.rooted {
			s.low[top.from] = min(s.low[top.from], s.low[top.object])
		}

		walk = walk[:len(walk)-1]
	}
}

// open records an object as reached and puts it on the component stack.
func (s *sorter) open(object Object) {
	s.index[object] = s.reached
	s.low[object] = s.reached
	s.reached++
	s.stack = append(s.stack, object)
	s.stacked[object] = true
}

// close takes the component rooted at an object off the stack and appends it to
// the result.
//
// Appending it here is what makes the order right: a component is closed only
// after everything reachable from it has been, and everything reachable from an
// object is what it needs.
func (s *sorter) close(root Object) {
	var component []Object

	for {
		last := len(s.stack) - 1
		member := s.stack[last]
		s.stack = s.stack[:last]
		s.stacked[member] = false
		component = append(component, member)

		if member == root {
			break
		}
	}

	// The members of a component came off the stack in the order the walk
	// happened to reach them, which is the one place the result could still
	// vary. Ordering them by name is what fixes it; the order within a cycle
	// cannot be right by any other measure, since that is what makes it a
	// cycle.
	slices.SortFunc(component, compareObjects)

	s.order.Objects = append(s.order.Objects, component...)

	if len(component) > 1 {
		s.order.Cycles = append(s.order.Cycles, component)
	}
}
