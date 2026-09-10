package catalog

import "slices"

// The model of a schema.
//
// It is the spine the reading phases fill in, and it grows with them: this
// carries what every object is identified by and what the phases after it have
// to hang things on. Fields arrive when the query that reads them does, so that
// no field is invented before there is a server answer to shape it.
//
// Two rules hold across all of it. Every identifier is a Name and every type is
// a TypeName, so a value that skipped folding cannot be stored. And every list
// has one order, applied by Sort, because a model whose order depends on how it
// was assembled compares unequal to itself — which is the diff reporting
// changes nobody made.

// Schema is one namespace and everything in it this version compares.
//
// What is not here is as deliberate as what is: functions, triggers, types and
// policies belong to a later phase of the product, and ADR-0007 requires an
// object that is not compared to be named as not compared rather than quietly
// left out. That list is kept where the diff shows it, not here.
type Schema struct {
	Name      Name
	Tables    []Table
	Sequences []Sequence
	Views     []View

	// Dependencies is what each object of the schema needs to already exist.
	// It is what Order turns into the sequence the DDL is written in, and it
	// is read rather than worked out from the text the model already holds:
	// telling which table a foreign key points at by reading its definition
	// means parsing SQL, and the server has already done that.
	Dependencies []Dependency
}

// Table is a table and everything defined on it.
type Table struct {
	Name        Name
	Columns     []Column
	Constraints []Constraint
	Indexes     []Index

	// Inherits names the tables this one inherits from, in the order they were
	// declared — which is significant, because it decides the order inherited
	// columns appear in, and is why this one list is not sorted.
	//
	// A parent in another schema is not named. The model is of one schema and
	// has no way to address outside it, which is the same boundary the
	// dependency graph draws.
	Inherits []Name

	// Partitioned reports that the table is divided into partitions, and
	// Partition that it is one of them. Both are true of a partition that is
	// itself partitioned.
	//
	// Neither models partitioning: this version does not compare it. They are
	// here so that what is not compared can be named as not compared, which
	// ADR-0007 requires and which matters more here than anywhere else — a
	// partitioned table written without its PARTITION BY is an ordinary table,
	// and a partition written without its bounds is a copy that silently holds
	// the wrong rows. The DDL writer declines them by name rather than
	// producing either.
	Partitioned bool
	Partition   bool

	// Unlogged is a table whose writes are not written ahead, and whose rows
	// are gone after a crash. It is a property of the table rather than a
	// tuning knob: recreating one as an ordinary table gives the copy
	// durability the original never had and the write cost that comes with it,
	// and recreating an ordinary one as unlogged silently throws away rows the
	// first time the server stops badly.
	Unlogged bool

	// RowSecurity reports that the table restricts which rows a reader sees,
	// and Forced that it does so even for the table's owner.
	//
	// Neither models the policies themselves — those are SYN-04 and this
	// version does not compare them. They are here for the reason
	// Partitioned is, and the stakes are higher: a table written without its
	// row security is a table whose every hidden row is visible in the copy,
	// and nothing about the copy says a control was dropped on the way. The
	// DDL writer declines such a table rather than writing a version of it
	// that reveals more than the original.
	RowSecurity bool
	Forced      bool

	// Options are the storage parameters declared on the table, as key=value,
	// ordered. fillfactor is the common one; autovacuum thresholds are the ones
	// somebody tuned for a reason nobody wrote down. A copy without them
	// behaves differently under load than the thing it was copied from.
	Options []string
}

// Column is one column of a table.
type Column struct {
	Name Name

	// Position is where the column comes in the table, counting from one over
	// the columns that are there. Columns are ordered by it and never by name,
	// because a model in an order no CREATE TABLE can produce is a model the
	// round trip can never close on.
	//
	// It is not attnum, and the difference is the point. A table that has had
	// a column dropped keeps the hole in attnum forever — PostgreSQL leaves
	// the entry in place so the row layout does not move — while its freshly
	// created copy has no hole. Comparing the numbers would report a
	// difference between a table and an exact copy of it, on every column
	// after the hole, which is the false positive this model exists to avoid.
	// What is part of the schema is the order; the numbering is bookkeeping.
	Position int

	Type    TypeName
	NotNull bool

	// Default is the expression as the server renders it. It is a string here
	// and canonicalised in the phase that can see what real servers answer
	// across the version matrix — inventing its canonical form now, from
	// nothing, is how a normalisation ends up wrong in a way only a diff
	// months later reveals.
	Default string

	// Identity is empty, "always" or "by default".
	Identity string

	// Generated is how a generated column is generated — "stored" today, which
	// is the only form PostgreSQL has — and empty when the column is not one.
	// The expression itself is in Default, which is where the catalog keeps
	// it: a generated column has a row in pg_attrdef like any default, and
	// what tells the two apart is this field rather than where the text lives.
	Generated string

	// Collation is the collation of the column, always explicit. A column that
	// does not declare one has the collation of the database, and comparing
	// "nothing" against that name would report a difference where there is
	// none.
	Collation Name
}

// ConstraintKind is what a constraint constrains.
type ConstraintKind string

// The kinds this version compares, spelled as the catalog spells them rather
// than as a number, so that a model rendered into a message reads.
const (
	ConstraintPrimaryKey ConstraintKind = "primary key"
	ConstraintForeignKey ConstraintKind = "foreign key"
	ConstraintUnique     ConstraintKind = "unique"
	ConstraintCheck      ConstraintKind = "check"
	ConstraintExclusion  ConstraintKind = "exclusion"
)

// Constraint is a rule declared on a table.
type Constraint struct {
	Name Name
	Kind ConstraintKind

	// Columns are the columns the constraint is declared on, in the order it
	// declares them — which matters for a key: (a, b) and (b, a) are different
	// constraints.
	Columns []Name

	// References is the table a foreign key points at, empty for every other
	// kind. It is structure rather than text because the writer has to act on
	// it: a key pointing at a table this version declines to write cannot be
	// written either, and finding that out by reading the definition would mean
	// parsing SQL to answer a question the catalog already answers.
	//
	// A key pointing outside the schema is left empty, the same boundary the
	// dependency graph draws: the model is of one schema and cannot address
	// beyond it.
	References Name

	// Definition is the constraint as the server renders it. It carries what
	// the fields above cannot: the ON DELETE of a foreign key, the expression
	// of a check, the operators of an exclusion.
	Definition string
}

// Index is an index on a table, whether it backs a constraint or stands alone.
type Index struct {
	Name    Name
	Unique  bool
	Primary bool

	// Columns are the indexed columns in order. An index by expression has no
	// column to name for that position, which is why the definition is kept.
	Columns []Name

	// Definition is the index as the server renders it, which is where a
	// partial index keeps its WHERE, an index by expression keeps its
	// expression, and an INCLUDE keeps its payload.
	Definition string
}

// Sequence is a sequence, whether it stands alone or belongs to a column.
//
// The numbers are not decoration. A sequence recreated with the default start
// and increment hands out numbers the original never would, and the destination
// of a sync ends up with a counter that collides with the rows already there —
// a data fault produced by a copy of the structure.
type Sequence struct {
	Name Name

	// Type is the integer type the sequence counts in, which is what decides
	// where it runs out: a sequence of smallint stops at 32767 whatever its
	// maximum says.
	Type TypeName

	// The bounds and the step, as the catalog holds them. They are read rather
	// than defaulted because every one of them can be declared, and a default
	// assumed here is a difference the diff cannot see and the sync silently
	// applies.
	Start     int64
	Increment int64
	Min       int64
	Max       int64

	// Cache is how many numbers a session claims at once. It changes what the
	// sequence hands out — a cache of twenty leaves gaps of twenty — so it is
	// part of the sequence rather than a tuning knob.
	Cache int64

	// Cycle is whether it starts over instead of failing when it reaches the
	// end, which is the difference between a counter that wraps and one that
	// stops the application.
	Cycle bool

	// OwnedBy is the column this sequence belongs to, empty for one that
	// stands alone. Losing it turns an identity column at the destination of a
	// sync into an orphaned sequence and a column with no default.
	OwnedBy ColumnRef
}

// ColumnRef names a column of a table in this schema.
//
// In this schema and not anywhere: PostgreSQL requires a sequence and the table
// that owns it to live in the same namespace, so the only reference the model
// carries today cannot point outside the schema being read.
type ColumnRef struct {
	Table  Name
	Column Name
}

// Valid reports whether the reference addresses a column.
func (r ColumnRef) Valid() bool { return r.Table.Valid() && r.Column.Valid() }

// View is a view and the query behind it.
type View struct {
	Name Name

	// Definition is the query as the server renders it, which is already
	// normalised: the server parses what was written and prints it back from
	// the parse tree, so two views written differently and meaning the same
	// come back the same.
	Definition string

	// Options are the storage parameters declared on the view, as key=value,
	// ordered and without the check option, which has a field of its own above
	// it in importance and below it here.
	//
	// security_barrier is why this is not cosmetic. A view declared with it
	// refuses to let a cheap function see rows the view was meant to hide, and
	// a copy made without it answers questions the original refused —
	// a change of security posture produced by a copy of a structure.
	// security_invoker, from PostgreSQL 15, decides whose rights the view reads
	// with, which is the same kind of difference.
	Options []string

	// CheckOption is "local", "cascaded" or empty, and it is here because the
	// definition does not carry it. WITH CHECK OPTION is stored beside the view
	// rather than inside its query, so a reader that asks only for the
	// definition drops the one clause that decides whether a write through the
	// view is refused — and the copy at the other end quietly accepts rows the
	// original would have rejected.
	CheckOption string
}

// ObjectKind is what sort of object a dependency names.
//
// Three kinds and not more, because the graph only relates the objects this
// model holds: a constraint and an index belong to a table and are created with
// it, and a column belongs to a table too — none of them is a thing that can be
// ordered independently of the table it is part of.
type ObjectKind string

// The kinds, spelled as the word rather than as the single character the
// catalog stores, so that an object rendered into a message reads.
const (
	ObjectTable    ObjectKind = "table"
	ObjectView     ObjectKind = "view"
	ObjectSequence ObjectKind = "sequence"
)

// Object names one object of the schema.
//
// The kind is carried alongside the name even though PostgreSQL would not let
// two of these share one: a table, a view and a sequence are all rows of
// pg_class and the name is unique across them. It is here because the order
// this feeds is read by the DDL writer, and "create sales.counter" is not a
// statement until something says which kind of thing it is.
type Object struct {
	Kind ObjectKind
	Name Name
}

// DependencyReason is why one object needs another to exist first.
//
// It is part of the edge rather than an annotation on it, because the writer
// acts on it: a circle of foreign keys is broken by creating the tables first
// and adding the keys afterwards, in ALTER TABLE statements of their own, and
// a writer holding edges with no reason cannot tell which ones it is allowed
// to defer. A circle of views has no such escape and must be reported instead.
type DependencyReason string

// The reasons, one per catalog table the dependency was recorded against.
const (
	// ReasonInheritance is a child table needing its parent, which covers
	// declarative partitioning as well: a partition is recorded the same way,
	// and the parent has to exist before either can be attached to it.
	ReasonInheritance DependencyReason = "inheritance"

	// ReasonForeignKey is a table needing the table its key points at.
	ReasonForeignKey DependencyReason = "foreign key"

	// ReasonQuery is a view needing what it selects from, whether that is a
	// table or another view.
	ReasonQuery DependencyReason = "query"

	// ReasonDefault is a table needing the sequence a column default calls.
	// It is the edge that puts CREATE SEQUENCE before CREATE TABLE, and the
	// reason the ownership recorded in Sequence.OwnedBy is not an edge at all:
	// OWNED BY needs the table, so it is written afterwards rather than
	// ordered before.
	ReasonDefault DependencyReason = "default"
)

// Dependency is one object of the schema needing another to exist first.
//
// It never names an object twice. A table whose foreign key points at itself
// and a view whose query refers to itself through a recursive CTE are both
// legal and both resolve where they stand, so an edge from an object to itself
// would be a cycle reported where there is nothing to order.
type Dependency struct {
	Object Object
	Needs  Object
	Reason DependencyReason
}

// Clone answers a copy of the schema that nobody else holds.
//
// A plain assignment almost does this, and that "almost" is the bug: the two
// values would share every slice, so appending a table to a copy would append
// it to the original — or worse, would not, depending on whether the slice had
// room, which is the shape of bug that reproduces on one machine and not on
// another.
//
// It exists because what a cache answers is shared and must not be modified.
// Nothing in this model has a method that alters it, so an ordinary reader
// needs no copy; this is for the caller that has one to edit, and it pays the
// whole allocation exactly once, where the editing is, instead of on every read
// for everyone. See ADR-0011 and Cache.
func (s Schema) Clone() Schema {
	copied := s

	copied.Tables = make([]Table, len(s.Tables))
	for i, table := range s.Tables {
		copied.Tables[i] = table.Clone()
	}

	copied.Sequences = slices.Clone(s.Sequences)
	copied.Dependencies = slices.Clone(s.Dependencies)

	copied.Views = make([]View, len(s.Views))
	for i, view := range s.Views {
		copied.Views[i] = view
		copied.Views[i].Options = slices.Clone(view.Options)
	}

	return copied
}

// Clone answers a copy of the table that nobody else holds.
func (t Table) Clone() Table {
	copied := t

	copied.Columns = slices.Clone(t.Columns)
	copied.Inherits = slices.Clone(t.Inherits)
	copied.Options = slices.Clone(t.Options)

	copied.Constraints = make([]Constraint, len(t.Constraints))
	for i, constraint := range t.Constraints {
		copied.Constraints[i] = constraint
		copied.Constraints[i].Columns = slices.Clone(constraint.Columns)
	}

	copied.Indexes = make([]Index, len(t.Indexes))
	for i, index := range t.Indexes {
		copied.Indexes[i] = index
		copied.Indexes[i].Columns = slices.Clone(index.Columns)
	}

	return copied
}

// Only answers a schema holding one object, and whether it was there.
//
// It exists so that the DDL of a single object is written by the same generator
// that writes the DDL of a schema. The alternative is a second writer for the
// panel that shows one table, and two writers of the same statements diverge —
// the day one of them learns about a storage parameter, the other is showing a
// definition that is quietly wrong.
//
// A table brings the sequences it owns, because a sequence a column defaults to
// is part of what that table is: without it the definition reads as calling
// something that does not exist. Nothing else follows. A foreign key pointing
// out of the slice is left on the table exactly as it was declared — it is what
// the object says about itself, and rewriting it to fit the slice would be
// showing somebody a definition their server does not hold.
//
// The dependencies come along only when both ends survive, which is the rule
// the ordering already applies to a model whose edges name something it does
// not hold.
func (s Schema) Only(name Name) (Schema, bool) {
	slice := Schema{Name: s.Name}

	for _, table := range s.Tables {
		if table.Name == name {
			slice.Tables = []Table{table.Clone()}
			slice.Sequences = ownedBy(s.Sequences, name)
		}
	}

	for _, view := range s.Views {
		if view.Name == name {
			copied := view
			copied.Options = slices.Clone(view.Options)
			slice.Views = []View{copied}
		}
	}

	for _, sequence := range s.Sequences {
		if sequence.Name == name && len(slice.Sequences) == 0 {
			slice.Sequences = []Sequence{sequence}
		}
	}

	if len(slice.Tables) == 0 && len(slice.Views) == 0 && len(slice.Sequences) == 0 {
		return Schema{}, false
	}

	slice.Dependencies = dependenciesWithin(s.Dependencies, objectsOf(slice))
	slice.Sort()

	return slice, true
}

// ownedBy answers copies of the sequences a table owns.
func ownedBy(sequences []Sequence, table Name) []Sequence {
	var owned []Sequence

	for _, sequence := range sequences {
		if sequence.OwnedBy.Table == table {
			owned = append(owned, sequence)
		}
	}

	return owned
}

// dependenciesWithin answers the edges whose both ends are in the slice.
func dependenciesWithin(edges []Dependency, held map[Object]bool) []Dependency {
	var within []Dependency

	for _, edge := range edges {
		if held[edge.Object] && held[edge.Needs] {
			within = append(within, edge)
		}
	}

	return within
}
