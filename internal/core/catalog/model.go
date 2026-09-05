package catalog

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
}

// Table is a table and everything defined on it.
type Table struct {
	Name        Name
	Columns     []Column
	Constraints []Constraint
	Indexes     []Index

	// Inherits names the tables this one inherits from, in the order they were
	// declared — which is significant, because it decides the order inherited
	// columns appear in.
	Inherits []Name
}

// Column is one column of a table.
type Column struct {
	Name Name

	// Position is attnum: the order the table has, which is the order the
	// server assigns and the order DDL has to reproduce. Columns are ordered by
	// it and never by name, because a model in an order no CREATE TABLE can
	// produce is a model the round trip can never close on.
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

	// Generated carries the expression of a generated column, empty when the
	// column is not one. It is canonicalised with Default, in the same phase
	// and for the same reason.
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

	// Definition is the constraint as the server renders it. It carries what
	// the fields above cannot yet: the referenced table of a foreign key, the
	// expression of a check, the operators of an exclusion. The phase that
	// reads constraints decides how much of it becomes structure and how much
	// stays text.
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
type Sequence struct {
	Name Name

	// OwnedBy is the column this sequence belongs to, empty for one that
	// stands alone. Losing it turns an identity column at the destination of a
	// sync into an orphaned sequence and a column with no default.
	OwnedBy ColumnRef
}

// ColumnRef names a column of a table in this schema.
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
}
