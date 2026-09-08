package ddl

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gsoares85/hermes/internal/core/catalog"
)

// Script is the DDL of a schema: the statements that build it, in an order
// that works, and what of it was not written.
//
// The second list is not an afterthought. A script that quietly leaves an
// object out produces a target that looks finished and is not, and the person
// running it has no way to know — so what could not be written is carried
// beside the statements and printed above them.
type Script struct {
	// Schema is the schema the statements were written from, and the one String
	// points a session at. The statements themselves name no schema at all, so
	// this is the only place the target appears — which is what lets the same
	// script build the same structure under another name, and why a script that
	// did not say so would apply itself to whatever the reader's path happened
	// to name.
	Schema catalog.Name

	// Statements are terminated by whoever runs them, not here: a caller that
	// sends them one at a time wants them without a semicolon, and String adds
	// one when it writes them out as a file.
	//
	// They do not include the statement that points the search path. A caller
	// running them one at a time has scoped the session itself and knows where
	// it is writing; String is for the other case, a file somebody opens later,
	// and that one has to carry its target.
	Statements []string

	// Omitted is what the model does not carry enough of to write, each with
	// the reason, ordered by object.
	Omitted []Omission
}

// Omission is one thing the script did not write, and why.
//
// Usually a whole object. Sometimes only a part of one that was written: a
// foreign key pointing at a table this version declines cannot be added, and a
// storage parameter of a shape this version will not interpolate cannot go
// back — while the table both are declared on is perfectly writable, so the
// table goes in the script and the part goes here.
//
// Which of the three it is has to be a field rather than something read off the
// others. It was inferred from Constraint being empty, and the day a second
// kind of part arrived that inference silently reclassified it as the whole
// object: the preview said a table had not been written three lines above
// writing it, and the round trip — which asks the same question to work out
// what to compare — dropped that table out of the property altogether.
type Omission struct {
	Object catalog.Object

	// Constraint names the rule that was left out, and Parameter the storage
	// parameter. Both are empty when what was left out is the object itself.
	Constraint catalog.Name
	Parameter  string

	Reason string
}

// Whole reports whether what was left out is the object rather than a part of
// it, which is the question both the preview and the round trip ask and the
// reason it is asked in one place.
func (o Omission) Whole() bool { return !o.Constraint.Valid() && o.Parameter == "" }

// String writes the script out as a file: where it applies, what was left out,
// and then the statements, each terminated.
//
// The target comes first and as a statement rather than a comment. Every
// identifier below it is bare, so the script builds into whichever schema the
// session's path names — and a file that did not say which would quietly apply
// itself to the first schema on whatever path the person running it happened to
// have, which is usually public and never what they meant. For a product whose
// first rule is that nothing destructive happens without a preview, the target
// of the operation cannot be the one thing the preview does not show.
func (s Script) String() string {
	var text strings.Builder

	if !s.Schema.Valid() && (len(s.Statements) > 0 || len(s.Omitted) > 0) {
		// Said rather than skipped. Without the path the file applies itself to
		// whatever schema the reader's session happens to name, and a file that
		// does that in silence is the failure the line below exists to prevent.
		text.WriteString("-- not written: the line that points the search path, because " +
			"the schema has no name this can write.\n" +
			"-- Point the session at the target before running any of this.\n\n")
	}

	if s.Schema.Valid() && len(s.Statements) > 0 {
		fmt.Fprintf(&text, "-- Every name below is written bare, so this builds into whatever\n"+
			"-- schema the search path names. Change the line below to build elsewhere.\n"+
			"SET search_path TO %s;\n\n", Ident(s.Schema))
	}

	for _, omission := range s.Omitted {
		// The names go through %q rather than raw, because an identifier may
		// hold a line break — PostgreSQL allows it inside quotes — and a
		// comment broken in half by one would comment out a statement.
		//
		// An omission naming a part is something left out of an object that was
		// written, and rendering it as though the object itself had been left
		// out is worse than saying nothing: the file would claim not to have
		// written a table three lines above writing it.
		switch {
		case omission.Constraint.Valid():
			fmt.Fprintf(&text, "-- not written: the constraint %q on the %s %q, because %s\n",
				omission.Constraint.String(), omission.Object.Kind,
				omission.Object.Name.String(), omission.Reason)
		case omission.Parameter != "":
			fmt.Fprintf(&text, "-- not written: the storage parameter %q of the %s %q, because %s\n",
				omission.Parameter, omission.Object.Kind,
				omission.Object.Name.String(), omission.Reason)
		default:
			fmt.Fprintf(&text, "-- not written: the %s %q, because %s\n",
				omission.Object.Kind, omission.Object.Name.String(), omission.Reason)
		}
	}

	if len(s.Omitted) > 0 && len(s.Statements) > 0 {
		text.WriteString("\n")
	}

	for _, statement := range s.Statements {
		text.WriteString(statement)
		text.WriteString(";\n\n")
	}

	return text.String()
}

// Of writes the DDL of a schema.
//
// The statements come in the order the model works out — everything after what
// it needs — and the two kinds of statement that cannot go there follow at the
// end: the foreign keys, which is how a circle of them is created at all, and
// the ownership of a sequence, which needs both the sequence and the table the
// column is on.
func Of(schema catalog.Schema) Script {
	writer := newWriter(schema)

	return writer.script()
}

// writer holds a schema and what has to be looked up while writing it.
type writer struct {
	schema catalog.Schema
	order  catalog.Order

	// tables by name, because a sequence has to find the column that owns it
	// to tell an identity column — which writes its own sequence — from one
	// with a default that calls it.
	tables map[catalog.Name]catalog.Table

	// owned is the sequence each column owns, which the identity clause needs
	// by column. It is a map rather than a search because it is asked once per
	// identity column: over a schema of a thousand tables the search was a
	// thousand walks of a thousand sequences, which is the kind of quadratic
	// that hides until the day somebody has a large schema.
	owned map[catalog.ColumnRef]catalog.Sequence

	// omitted is every object not written, by the reason it was not.
	omitted map[catalog.Object]string

	// declined are the rules left out of objects that were written, gathered as
	// the statements are built rather than worked out beforehand.
	declined []Omission
}

func newWriter(schema catalog.Schema) *writer {
	writer := &writer{
		schema:  schema,
		order:   schema.Order(),
		tables:  make(map[catalog.Name]catalog.Table, len(schema.Tables)),
		owned:   make(map[catalog.ColumnRef]catalog.Sequence, len(schema.Sequences)),
		omitted: map[catalog.Object]string{},
	}

	for _, table := range schema.Tables {
		writer.tables[table.Name] = table
	}

	for _, sequence := range schema.Sequences {
		if sequence.OwnedBy.Valid() {
			writer.owned[sequence.OwnedBy] = sequence
		}
	}

	writer.findOmissions()

	return writer
}

// script writes every object and then everything that had to wait.
//
// The statements are worked out from the schema's own lists and then placed by
// the order the model computed, rather than the order being walked and each
// object looked up by name. Both produce the same script; only this one has no
// "and what if it is not there" branch, which would be a branch nothing can
// reach and nobody can test.
func (w *writer) script() Script {
	creates, follows := w.statements()
	script := Script{Schema: w.schema.Name, Omitted: w.omissions()}

	for _, object := range w.order.Objects {
		script.Statements = append(script.Statements, creates[object]...)
	}

	for _, object := range w.order.Objects {
		script.Statements = append(script.Statements, follows[object]...)
	}

	return script
}

// statements answers what creates each object and what has to follow every
// creation, both keyed by the object they belong to.
//
// The two are separated because none of the followers can go where the object
// is created. A foreign key cannot, because a circle of them is not creatable in
// any order; a constraint declared NOT VALID cannot, because CREATE TABLE
// accepts the words and validates it anyway; the ownership of a sequence cannot,
// because the table needs the sequence — its default calls it — and OWNED BY
// needs the table.
func (w *writer) statements() (creates, follows map[catalog.Object][]string) {
	creates = map[catalog.Object][]string{}
	follows = map[catalog.Object][]string{}

	for _, sequence := range w.schema.Sequences {
		object := catalog.Object{Kind: catalog.ObjectSequence, Name: sequence.Name}
		if w.omitted[object] != "" || w.identityOwned(sequence) {
			continue
		}

		creates[object] = []string{w.sequence(sequence)}

		if sequence.OwnedBy.Valid() {
			follows[object] = []string{ownership(sequence)}
		}
	}

	for _, table := range w.schema.Tables {
		object := catalog.Object{Kind: catalog.ObjectTable, Name: table.Name}
		if w.omitted[object] != "" {
			continue
		}

		creates[object] = w.table(table)
		follows[object] = w.afterTheTable(table)
	}

	for _, view := range w.schema.Views {
		object := catalog.Object{Kind: catalog.ObjectView, Name: view.Name}
		if w.omitted[object] != "" {
			continue
		}

		creates[object] = []string{w.view(view)}
	}

	return creates, follows
}

// findOmissions works out what cannot be written, and then what cannot be
// written because of that.
//
// The second half is what the dependency graph is for. A view over a table the
// script declined is a statement that fails on the target, halfway through, on
// a database that is now part built — so it is declined here, where the reason
// can be given, rather than left to the server to discover.
//
// It runs to a fixed point rather than once down the order, because a cycle has
// no order: two tables that need each other are reached in one pass in one
// direction only, and the second of them would keep a statement that cannot
// run.
func (w *writer) findOmissions() {
	for _, table := range w.schema.Tables {
		object := catalog.Object{Kind: catalog.ObjectTable, Name: table.Name}

		switch {
		case table.RowSecurity || table.Forced:
			// The gravest of the declines, and the reason it is first. A table
			// written without its row security is a table whose every hidden
			// row is readable in the copy, and nothing about the copy says a
			// control was dropped on the way — the policies are not compared by
			// this version, so there is no version of this table that can be
			// written honestly.
			w.omitted[object] = "it restricts which rows are visible, and this version does not compare policies"
		case table.Partitioned:
			w.omitted[object] = "it is divided into partitions, and this version does not compare partitioning"
		case table.Partition:
			w.omitted[object] = "it is a partition, and this version does not compare partitioning"
		default:
			w.declineUnknownGeneration(object, table)
			w.declineUnwritableSecurity(object, table.Options)
		}
	}

	for _, view := range w.schema.Views {
		w.declineUnwritableSecurity(
			catalog.Object{Kind: catalog.ObjectView, Name: view.Name}, view.Options)
	}

	// The two ways a decline spreads, together, because either can create work
	// for the other and running them in turn does not converge.
	//
	// A sequence goes with the table that owns it, and the graph cannot say so:
	// the ownership edge is deliberately absent — reading it as an ordering
	// constraint would make every serial column a cycle. That was decided
	// beside the loop below and ran before it, which meant a table declined by
	// the propagation itself never reached the sequence check. Row level
	// security made that reachable by adding a second way for a table to be
	// declined, and the script went back to writing an ALTER SEQUENCE … OWNED BY
	// against a table it had said three lines above that it did not write.
	for spread := true; spread; {
		spread = false

		for _, sequence := range w.schema.Sequences {
			if !sequence.OwnedBy.Valid() {
				continue
			}

			owned := catalog.Object{Kind: catalog.ObjectSequence, Name: sequence.Name}
			owner := catalog.Object{Kind: catalog.ObjectTable, Name: sequence.OwnedBy.Table}

			if w.omitted[owned] != "" || w.omitted[owner] == "" {
				continue
			}

			w.omitted[owned] = fmt.Sprintf("the table %q that owns it is not written",
				sequence.OwnedBy.Table.String())
			spread = true
		}

		for _, edge := range w.schema.Dependencies {
			if !blocking(edge) || w.omitted[edge.Object] != "" || w.omitted[edge.Needs] == "" {
				continue
			}

			w.omitted[edge.Object] = fmt.Sprintf("it needs the %s %q, which is not written",
				edge.Needs.Kind, edge.Needs.Name.String())
			spread = true
		}
	}
}

// blocking reports whether an object that needs a declined one is itself
// impossible to write.
//
// Not every dependency is. A foreign key is already written afterwards, in a
// statement of its own, so a table whose only tie to a declined one is a key can
// be created perfectly well — the key is what has to go, and it goes on its own.
// Treating it as blocking made one declined table take out everything that
// referenced it, and everything that referenced those, until a schema with a
// partitioned table anywhere near the middle wrote almost nothing.
//
// The reason is on the edge precisely so this can be asked. A view has no such
// escape: its query names the object and there is no later statement to move it
// to. Inheritance and a default that calls a sequence are the same — both are
// clauses of the CREATE TABLE itself.
func blocking(edge catalog.Dependency) bool {
	return edge.Reason != catalog.ReasonForeignKey
}

// declineUnknownGeneration declines a table with a column generated in a way
// this build does not know.
//
// The reader carries an unrecognised form through rather than dropping it, which
// is right: a spelling from a version newer than this one is still a fact about
// the column. The writer cannot do the same. Interpolating it produces
// GENERATED ALWAYS AS (...) followed by whatever the catalog said, which is not
// a statement — so the choice is between a script that fails on the target and a
// table declined by name here, and ADR-0007 already answers that.
func (w *writer) declineUnknownGeneration(object catalog.Object, table catalog.Table) {
	for _, column := range table.Columns {
		if column.Generated == "" || column.Generated == generatedStored {
			continue
		}

		w.omitted[object] = fmt.Sprintf(
			"its column %q is generated %q, which this version does not know how to write",
			column.Name.String(), column.Generated)

		return
	}
}

// declineUnwritableSecurity declines an object whose refused storage parameter
// decides who sees which rows.
//
// An ordinary parameter that cannot be written is named in the preview and the
// object is written without it, which is right: fillfactor is a tuning knob and
// a copy without it is a copy that performs differently. security_barrier and
// security_invoker are not knobs. A view written without security_barrier lets a
// cheap function see the rows the view exists to hide, and the copy says nothing
// about it — which is the same fault row level security is declined for, and
// declining one while degrading the other was an asymmetry with no reason
// behind it.
//
// Nothing reaches it today: both take a boolean, and a boolean is a shape the
// writer accepts. It is here because that is a fact about today's server rather
// than a rule, and the version of this file that reasoned from such facts has
// had to be corrected four times.
func (w *writer) declineUnwritableSecurity(object catalog.Object, options []string) {
	_, refused := storage(options)

	for _, parameter := range refused {
		name, _, _ := strings.Cut(parameter, "=")

		if securityParameters[name] {
			w.omitted[object] = fmt.Sprintf(
				"its %q is not a shape this version writes, and a copy without it shows"+
					" what the original hides", name)

			return
		}
	}
}

// securityParameters are the storage parameters that decide who sees what,
// rather than how fast.
var securityParameters = map[string]bool{
	"security_barrier": true,
	"security_invoker": true,
}

// generatedStored is the only way PostgreSQL generates a column today. A model
// holding anything else came from a newer server than this build knows.
const generatedStored = "stored"

// omissions is what was not written: the objects first, in the order the schema
// holds them, then the rules left out of objects that were.
//
// It runs after the statements are built, because the second list is gathered
// while they are — a foreign key is known to be unwritable at the point it would
// have been written.
func (w *writer) omissions() []Omission {
	if len(w.omitted) == 0 && len(w.declined) == 0 {
		return nil
	}

	omitted := make([]Omission, 0, len(w.omitted)+len(w.declined))

	for _, object := range w.order.Objects {
		if reason := w.omitted[object]; reason != "" {
			omitted = append(omitted, Omission{Object: object, Reason: reason})
		}
	}

	// Already in one order: they were gathered walking the schema's tables and
	// each table's constraints, both of which Sort left ordered.
	return append(omitted, w.declined...)
}

// sequence writes a CREATE SEQUENCE.
//
// Not every sequence gets one — the one behind an identity column is created by
// the column's own clause, and statements leaves it out — but the ones that do
// are all the same statement.
func (w *writer) sequence(sequence catalog.Sequence) string {
	return options("CREATE SEQUENCE "+Ident(sequence.Name), sequenceOptions(sequence)...)
}

// sequenceOptions is everything about a sequence, which is what CREATE
// SEQUENCE takes.
func sequenceOptions(sequence catalog.Sequence) []string {
	return append([]string{"AS " + sequence.Type.String()}, sequenceCounters(sequence)...)
}

// sequenceCounters are the parameters that decide what a sequence hands out,
// without the type it counts in.
//
// Without it because an identity clause refuses it: the sequence behind an
// identity column counts in the type of the column, and saying so again is
// rejected as redundant rather than ignored.
//
// All the rest, always, none defaulted. A sequence recreated with the default
// start and step hands out numbers the original never would, and the copy ends
// up colliding with rows that are already there — a data fault produced by a
// copy of the structure, and one nothing in a structure diff would show.
func sequenceCounters(sequence catalog.Sequence) []string {
	cycle := ""
	if sequence.Cycle {
		cycle = "CYCLE"
	}

	return []string{
		"INCREMENT BY " + strconv.FormatInt(sequence.Increment, 10),
		"MINVALUE " + strconv.FormatInt(sequence.Min, 10),
		"MAXVALUE " + strconv.FormatInt(sequence.Max, 10),
		"START WITH " + strconv.FormatInt(sequence.Start, 10),
		"CACHE " + strconv.FormatInt(sequence.Cache, 10),
		cycle,
	}
}

// ownership ties a sequence to the column that owns it, after both exist.
//
// It is a statement of its own rather than a clause of the CREATE SEQUENCE, and
// that is what keeps the two out of a cycle: the table needs the sequence,
// because its default calls it, and OWNED BY needs the table. Written as one
// statement they would need each other.
func ownership(sequence catalog.Sequence) string {
	return "ALTER SEQUENCE " + Ident(sequence.Name) + " OWNED BY " + qualify(sequence.OwnedBy)
}

// storage writes the parameters declared on a table or a view.
//
// They are written back because a copy without them behaves differently from
// the thing it was copied from: fillfactor and the autovacuum thresholds under
// load, and on a view security_barrier, which decides whether a cheap function
// gets to see the rows the view was meant to hide.
//
// The text comes from the catalog as key=value and the shape is checked before
// it goes back.
//
// The first version of this said a storage parameter is a name the server
// defines and that there is no set of them a person can extend. That is not
// true: the extension API exports add_string_reloption, and pg_class.reloptions
// is plain text. It was the third time on this branch that an unverified
// invariant was written as though it were a property of PostgreSQL, after "an
// exact operator always exists" and "the corpus covers the pairs" — both of
// which had to be retracted.
//
// So it is checked instead of asserted, and checked by naming the values a
// parameter may have rather than by naming the characters that would end a
// statement. The second is what the version before this one did, under a
// comment saying a value had nothing in it that could end the statement — and
// it accepted an empty value, which produces WITH (ext.a=), and a value of two
// hyphens, which comments out the closing parenthesis and the semicolon and
// swallows the statement after it.
func storage(options []string) (string, []string) {
	var written, refused []string

	for _, option := range options {
		if storageParameter.MatchString(option) {
			written = append(written, option)

			continue
		}

		refused = append(refused, option)
	}

	if len(written) == 0 {
		return "", refused
	}

	return "WITH (" + strings.Join(written, ", ") + ")", refused
}

// storageParameter is what a storage parameter may look like: an optionally
// qualified bare name, then either a number or a bare word — which is every
// value a stock server stores, fillfactor=70 and 2.5 and -1 and false and
// check_option=local included. The hyphen is a sign and only a sign, because
// log_autovacuum_min_duration=-1 needs one and a value of nothing but hyphens
// is a comment.
//
// Being too narrow here is loud: the parameter is named in the preview instead
// of written, which somebody reads. Being too wide is what put a comment marker
// into a statement.
var storageParameter = regexp.MustCompile(
	`^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)?=` +
		`(-?[0-9]+(\.[0-9]+)?|[a-zA-Z][a-zA-Z0-9_]*)$`)

// declineParameters records the storage parameters that were not written.
//
// Not writing one in silence would be the quiet half of the fault: a copy
// behaving differently under load than the thing it was copied from, with
// nothing to say why. Naming it is what ADR-0007 asks of anything not compared,
// and the preview is where somebody sees it.
func (w *writer) declineParameters(object catalog.Object, refused []string) {
	for _, parameter := range refused {
		w.declined = append(w.declined, Omission{
			Object:    object,
			Parameter: parameter,
			Reason:    "it is not a shape this version writes",
		})
	}
}

// table writes a CREATE TABLE and the indexes that stand on their own.
func (w *writer) table(table catalog.Table) []string {
	kind := "CREATE TABLE "
	if table.Unlogged {
		kind = "CREATE UNLOGGED TABLE "
	}

	parameters, refused := storage(table.Options)
	w.declineParameters(catalog.Object{Kind: catalog.ObjectTable, Name: table.Name}, refused)

	statements := []string{clauses(
		kind+Ident(table.Name),
		body(w.contents(table)),
		inherits(table),
		parameters,
	)}

	for _, index := range table.Indexes {
		// The definition the server rendered is the whole statement, and it is
		// written as it came. Taking it apart into a name, a method and a list
		// of columns would lose what only the text carries — the expression of
		// an index by expression, the WHERE of a partial one, the payload of an
		// INCLUDE — and putting it back together is a second implementation of
		// a renderer the server already has.
		statements = append(statements, index.Definition)
	}

	return statements
}

// contents are the columns and the constraints that go inside the parentheses.
//
// Every constraint but the two kinds that cannot go there. Foreign keys are
// added afterwards because a circle of them cannot be written any other way —
// and once one has to be deferred, deferring all of them keeps the script from
// depending on which key happened to be in a circle. A constraint declared NOT
// VALID is the other: CREATE TABLE accepts the words and ignores them.
func (w *writer) contents(table catalog.Table) []string {
	contents := make([]string, 0, len(table.Columns)+len(table.Constraints))

	for _, column := range table.Columns {
		contents = append(contents, w.column(table, column))
	}

	for _, constraint := range table.Constraints {
		if constraint.Kind != catalog.ConstraintForeignKey && !deferred(constraint) {
			contents = append(contents, constraintClause(constraint))
		}
	}

	return contents
}

// column writes one column of a table.
//
// The type goes into the statement as it stands, and it is the one field here
// that neither quotes nor checks. It cannot do either: a type is not an
// identifier — character varying(10)[] and numeric(10,2) and timestamp with
// time zone are all one type name each — so quoting it would break every type
// that has a modifier, and a pattern for what a legal one looks like would be
// the permit list this package has already had to retract twice.
//
// What makes it safe is where it comes from. Every type in a model read by this
// product is format_type's own rendering, folded by NewTypeName, and a server
// does not render a type that cannot be written back. TypeName's own doc names
// a second origin — a comparison target somebody wrote by hand — and whoever
// adds that owes the same guarantee at the point the model is built, because
// there is nowhere here to recover it.
func (w *writer) column(table catalog.Table, column catalog.Column) string {
	notNull := ""
	if column.NotNull {
		notNull = "NOT NULL"
	}

	return clauses(
		Ident(column.Name),
		column.Type.String(),
		collate(column),
		w.generation(table, column),
		notNull,
	)
}

// collate writes the collation of a column, which the model always holds
// explicitly.
//
// A column of a type that cannot be collated has none, and writing COLLATE for
// it is an error rather than a no-op — which is why this asks the model instead
// of assuming every column has one.
func collate(column catalog.Column) string {
	if !column.Collation.Valid() {
		return ""
	}

	return "COLLATE " + Ident(column.Collation)
}

// generation is where a column's value comes from: an identity, a generated
// expression, or a default.
//
// One of the three at most, which is the server's rule as well: a column cannot
// be an identity and have a default, and a generated column keeps its
// expression where a default would be, told apart by Column.Generated rather
// than by where the text is.
func (w *writer) generation(table catalog.Table, column catalog.Column) string {
	switch {
	case column.Identity != "":
		return w.identity(table, column)
	case column.Generated == generatedStored:
		// Parenthesised on the way out whatever the server rendered, because
		// the syntax requires it and the rendering only supplies it for an
		// expression that needed it. Two sets of parentheses are harmless; the
		// server prints them back as one.
		//
		// Only the form this build knows reaches here: a table with a column
		// generated any other way is declined whole, because there is no way to
		// write it and no way to leave it out of a table it is a column of.
		return "GENERATED ALWAYS AS (" + column.Default + ") " + strings.ToUpper(column.Generated)
	case column.Default != "":
		return "DEFAULT " + column.Default
	default:
		return ""
	}
}

// identity writes the clause that both declares a column an identity and
// creates the sequence behind it.
//
// The sequence goes in by name and with every parameter, rather than being left
// to the server. A column declared START WITH 5 keeps that number nowhere else,
// and a table renamed after the fact keeps a sequence named after what it used
// to be called — neither survives a clause that says only AS IDENTITY.
func (w *writer) identity(table catalog.Table, column catalog.Column) string {
	clause := "GENERATED " + strings.ToUpper(column.Identity) + " AS IDENTITY"

	counter, known := w.owned[catalog.ColumnRef{Table: table.Name, Column: column.Name}]
	if !known {
		return clause
	}

	inside := append([]string{"SEQUENCE NAME " + Ident(counter.Name)}, sequenceCounters(counter)...)

	return clause + " (" + strings.Join(present(inside), " ") + ")"
}

// constraintClause writes a constraint as it goes inside a CREATE TABLE.
//
// The definition comes from the server, rendered from the parse tree, and it is
// written as it came for the reason an index definition is: it carries the
// referenced table of a key, the expression of a check and the operators of an
// exclusion, and rebuilding those from the fields beside it would be a second
// implementation of a renderer that already exists.
func constraintClause(constraint catalog.Constraint) string {
	return "CONSTRAINT " + Ident(constraint.Name) + " " + constraint.Definition
}

// afterTheTable writes the constraints that could not go inside it: every
// foreign key, and anything declared NOT VALID.
func (w *writer) afterTheTable(table catalog.Table) []string {
	var statements []string

	for _, constraint := range table.Constraints {
		if constraint.Kind == catalog.ConstraintForeignKey && w.declinedKey(table, constraint) {
			continue
		}

		if constraint.Kind != catalog.ConstraintForeignKey && !deferred(constraint) {
			continue
		}

		statements = append(statements, options(
			"ALTER TABLE "+Ident(table.Name),
			"ADD "+constraintClause(constraint),
		))
	}

	return statements
}

// deferred reports whether a constraint has to be added by ALTER TABLE because
// CREATE TABLE cannot express it.
//
// NOT VALID is that case, and it fails quietly: CREATE TABLE parses the words
// and creates the constraint validated anyway. Two things go wrong at once. The
// copy renders back without them, so the model of the copy differs from the
// model it came from and the diff reports a change nobody made. And the server
// scans the whole table to validate a constraint somebody deliberately declared
// without validating — a scan and a lock nobody asked for, on the tables large
// enough that NOT VALID was worth writing in the first place.
//
// It is read off the rendered definition rather than from a field of its own
// because that is where the server puts it, at the end and after everything
// else the definition holds.
func deferred(constraint catalog.Constraint) bool {
	return strings.HasSuffix(constraint.Definition, " NOT VALID")
}

// declinedKey reports whether a foreign key points at something the script did
// not write, and records it as left out when it does.
//
// The key is what has to go, and only the key. The table it is declared on is
// writable — its other keys included — which is why declining the table for it
// would take out everything referencing that table, and everything referencing
// those, until a schema with a partitioned table near the middle wrote almost
// nothing.
func (w *writer) declinedKey(table catalog.Table, key catalog.Constraint) bool {
	target := catalog.Object{Kind: catalog.ObjectTable, Name: key.References}

	reason := w.omitted[target]
	if !key.References.Valid() || reason == "" {
		return false
	}

	w.declined = append(w.declined, Omission{
		Object:     catalog.Object{Kind: catalog.ObjectTable, Name: table.Name},
		Constraint: key.Name,
		Reason: fmt.Sprintf("it points at the table %q, which is not written",
			key.References.String()),
	})

	return true
}

// inherits writes the parents of a table, in the order they were declared.
//
// The order is not decoration: it decides where the inherited columns come, so
// naming the parents the other way round produces a table whose columns are in
// another order, which the round trip would see.
func inherits(table catalog.Table) string {
	if len(table.Inherits) == 0 {
		return ""
	}

	parents := make([]string, 0, len(table.Inherits))
	for _, parent := range table.Inherits {
		parents = append(parents, Ident(parent))
	}

	return "INHERITS (" + strings.Join(parents, ", ") + ")"
}

// view writes a CREATE VIEW.
//
// The check option is written after the query and not inside it, which is where
// the server keeps it too — a view that only carried its query would accept
// writes the original refuses.
func (w *writer) view(view catalog.View) string {
	check := ""
	if view.CheckOption != "" {
		check = "WITH " + strings.ToUpper(view.CheckOption) + " CHECK OPTION"
	}

	parameters, refused := storage(view.Options)
	w.declineParameters(catalog.Object{Kind: catalog.ObjectView, Name: view.Name}, refused)

	return options(clauses("CREATE VIEW "+Ident(view.Name), parameters, "AS"),
		view.Definition, check)
}

// identityOwned reports whether the column that owns this sequence is an
// identity column, which is the one case the sequence is not written.
func (w *writer) identityOwned(sequence catalog.Sequence) bool {
	// A sequence that stands alone and one whose table the schema does not hold
	// both come out of the map as a table with no columns, which no column
	// matches — so neither needs a test of its own.
	return slices.ContainsFunc(w.tables[sequence.OwnedBy.Table].Columns,
		func(column catalog.Column) bool {
			return column.Name == sequence.OwnedBy.Column && column.Identity != ""
		})
}
