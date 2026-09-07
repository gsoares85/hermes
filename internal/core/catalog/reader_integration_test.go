//go:build integration

package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
	"github.com/gsoares85/hermes/internal/driver"
	"github.com/gsoares85/hermes/internal/testsupport"
)

// openSession brings up a server of the given version and checks out a session
// against it.
func openSession(t *testing.T, version string) (driver.Session, *testsupport.Instance) {
	t.Helper()

	instance := testsupport.SharedPostgres(t, version)

	return testsupport.Session(t, instance), instance
}

// readCorpus creates the pathological schema on a server of the given version
// and reads it back.
func readCorpus(t *testing.T, version string) catalog.Schema {
	t.Helper()

	session, instance := openSession(t, version)
	schema := testsupport.Corpus(t, instance)

	read, err := catalog.NewReader(session).Read(t.Context(), catalog.NewName(schema))
	if err != nil {
		t.Fatalf("reading %s on PostgreSQL %s: %v", schema, version, err)
	}

	return read
}

// tableIn finds a table by name, failing the test when it is not there.
func tableIn(t *testing.T, schema catalog.Schema, name string) catalog.Table {
	t.Helper()

	for _, table := range schema.Tables {
		if table.Name.String() == name {
			return table
		}
	}

	t.Fatalf("the schema has no table %q; it has %v", name, names(schema))

	return catalog.Table{}
}

func names(schema catalog.Schema) []string {
	found := make([]string, 0, len(schema.Tables))
	for _, table := range schema.Tables {
		found = append(found, table.Name.String())
	}

	return found
}

// columnIn finds a column by name, failing the test when it is not there.
func columnIn(t *testing.T, table catalog.Table, name string) catalog.Column {
	t.Helper()

	for _, column := range table.Columns {
		if column.Name.String() == name {
			return column
		}
	}

	t.Fatalf("the table %s has no column %q", table.Name, name)

	return catalog.Column{}
}

// The corpus is read on every version the product supports, and what comes back
// has to be the same model on all of them.
//
// This is the assertion the whole task is arranged around: the reader is not
// allowed to answer differently because the server is older or newer. A
// difference here is a false positive waiting in the diff, and finding it now
// costs a test run rather than a user's trust.
//
// Nothing is neutralised before the comparison any more. The readings used to
// go through a helper that took the name of the corpus schema out of every
// rendered default and definition, because the fixture numbers its schema and
// each version therefore reads one under a different name — and that helper was
// hiding the very thing it was working around, since a real diff between an
// environment and its copy is exactly two schemas with different names. The
// search path the read scopes is what makes the substitution unnecessary, and
// deleting it is how this test proves it.
func TestTheCorpusReadsTheSameOnEveryVersion(t *testing.T) {
	t.Parallel()

	var first catalog.Schema

	firstVersion := testsupport.SupportedVersions[0]

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			read := readCorpus(t, version)

			if version == firstVersion {
				first = read

				return
			}

			if difference := describeAcrossVersions(first, read); difference != "" {
				t.Errorf("PostgreSQL %s reads the corpus differently from %s: %s",
					version, firstVersion, difference)
			}
		})
	}
}

// describeAcrossVersions is describe without the one thing two different
// servers do not yet agree on: the text of a view definition.
//
// PostgreSQL 16 changed how it renders a view back. Up to 15 it qualifies a
// column with the relation it came from — lower(split_part(active_orders.email,
// '@', 2)) — and from 16 it leaves the qualification out where nothing is
// ambiguous. Both are the same query, and neither is wrong; what is wrong is
// this model holding two spellings for it, because a diff between a server on
// 15 and its copy on 17 would report every view as changed.
//
// Closing that is the canonicalisation phase's work and it is not a substring
// away: telling a qualification that can be dropped from one that carries
// meaning is a question about the query, not about its text. Until then the
// matrix compares everything about a view except the words, and
// TestAViewDefinitionStillDependsOnTheServerVersion holds the gap in place so
// that it is a known one rather than a forgotten one.
func describeAcrossVersions(want, got catalog.Schema) string {
	return describe(withoutViewDefinitions(want), withoutViewDefinitions(got))
}

func withoutViewDefinitions(schema catalog.Schema) catalog.Schema {
	stripped := schema
	stripped.Views = make([]catalog.View, len(schema.Views))

	for i, view := range schema.Views {
		view.Definition = ""
		stripped.Views[i] = view
	}

	return stripped
}

// describe answers what differs between two readings, in the first place they
// differ, so that a failure names the column rather than printing two models.
//
// It compares the rendered text as well — the default of a column, the
// definition of a constraint, of an index and of a view — because two readings
// of one server have to agree on all of it.
func describe(want, got catalog.Schema) string {
	if len(want.Tables) != len(got.Tables) {
		return report("tables", names(want), names(got))
	}

	for i := range want.Tables {
		if difference := describeTable(want.Tables[i], got.Tables[i]); difference != "" {
			return difference
		}
	}

	if difference := describeSequences(want, got); difference != "" {
		return difference
	}

	if difference := describeViews(want, got); difference != "" {
		return difference
	}

	return describeDependencies(want, got)
}

// The graph is compared like everything else, and for the same reason: it is
// part of the model the diff will compare, so two readings that disagree about
// it are two readings that disagree.
//
// Edges are compared with ==, which they can be: an edge is two objects and a
// reason, and none of them carries a slice.
func describeDependencies(want, got catalog.Schema) string {
	if len(want.Dependencies) != len(got.Dependencies) {
		return report("dependencies", want.Dependencies, got.Dependencies)
	}

	for i := range want.Dependencies {
		if want.Dependencies[i] != got.Dependencies[i] {
			return report("the dependency of "+want.Dependencies[i].Object.Name.String(),
				want.Dependencies[i], got.Dependencies[i])
		}
	}

	return ""
}

// A sequence is compared with ==, which it can be: it carries no slice, so a
// field added to it without a thought for how it compares breaks here rather
// than quietly stopping being compared. A view carries its storage parameters
// and so has a comparison of its own.
func describeSequences(want, got catalog.Schema) string {
	if len(want.Sequences) != len(got.Sequences) {
		return report("sequences", sequenceNames(want), sequenceNames(got))
	}

	for i := range want.Sequences {
		if want.Sequences[i] != got.Sequences[i] {
			return report("the sequence "+want.Sequences[i].Name.String(),
				want.Sequences[i], got.Sequences[i])
		}
	}

	return ""
}

func describeViews(want, got catalog.Schema) string {
	if len(want.Views) != len(got.Views) {
		return report("views", viewNames(want), viewNames(got))
	}

	for i := range want.Views {
		if !slices.Equal(want.Views[i].Options, got.Views[i].Options) {
			return report("the storage parameters of the view "+want.Views[i].Name.String(),
				want.Views[i].Options, got.Views[i].Options)
		}

		if !sameView(want.Views[i], got.Views[i]) {
			return report("the view "+want.Views[i].Name.String(), want.Views[i], got.Views[i])
		}
	}

	return ""
}

// sameView compares two views without their storage parameters, which the
// caller has already compared and reported more precisely.
func sameView(want, got catalog.View) bool {
	return want.Name == got.Name &&
		want.Definition == got.Definition &&
		want.CheckOption == got.CheckOption
}

func sequenceNames(schema catalog.Schema) []string {
	found := make([]string, 0, len(schema.Sequences))
	for _, sequence := range schema.Sequences {
		found = append(found, sequence.Name.String())
	}

	return found
}

func viewNames(schema catalog.Schema) []string {
	found := make([]string, 0, len(schema.Views))
	for _, view := range schema.Views {
		found = append(found, view.Name.String())
	}

	return found
}

func describeTable(want, got catalog.Table) string {
	if want.Name != got.Name {
		return report("table", want.Name, got.Name)
	}
	if len(want.Columns) != len(got.Columns) {
		return report("the columns of "+want.Name.String(), len(want.Columns), len(got.Columns))
	}

	for i := range want.Columns {
		if want.Columns[i] != got.Columns[i] {
			return report(want.Name.String()+"."+want.Columns[i].Name.String(),
				want.Columns[i], got.Columns[i])
		}
	}

	if difference := describeConstraints(want, got); difference != "" {
		return difference
	}

	if difference := describeIndexes(want, got); difference != "" {
		return difference
	}

	return describeTableItself(want, got)
}

// describeTableItself compares what a table is rather than what is defined on
// it: the parents it inherits and whether it takes part in partitioning.
//
// Left out of the comparison, these three fields were exempt from the three
// properties this file exists to hold — that two schemas with one structure read
// the same, that two connections read the same, and that six versions read the
// same. The order of the parents comes from an aggregate with an ORDER BY and
// relispartition is read straight from pg_class: both are exactly the kind of
// thing that could differ between 12 and 18 and be believed because nothing
// looked.
func describeTableItself(want, got catalog.Table) string {
	if want.Unlogged != got.Unlogged {
		return report(want.Name.String()+" unlogged", want.Unlogged, got.Unlogged)
	}

	if !slices.Equal(want.Options, got.Options) {
		return report(want.Name.String()+" storage parameters", want.Options, got.Options)
	}

	if want.Partitioned != got.Partitioned || want.Partition != got.Partition {
		return report(want.Name.String()+" partitioning",
			[]bool{want.Partitioned, want.Partition},
			[]bool{got.Partitioned, got.Partition})
	}

	if !slices.Equal(want.Inherits, got.Inherits) {
		return report(want.Name.String()+" inherits", want.Inherits, got.Inherits)
	}

	return ""
}

func describeConstraints(want, got catalog.Table) string {
	if len(want.Constraints) != len(got.Constraints) {
		return report("the constraints of "+want.Name.String(),
			constraintNames(want), constraintNames(got))
	}

	for i := range want.Constraints {
		if !sameConstraint(want.Constraints[i], got.Constraints[i]) {
			return report(want.Name.String()+" constraint "+want.Constraints[i].Name.String(),
				want.Constraints[i], got.Constraints[i])
		}
	}

	return ""
}

func describeIndexes(want, got catalog.Table) string {
	if len(want.Indexes) != len(got.Indexes) {
		return report("the indexes of "+want.Name.String(), indexNames(want), indexNames(got))
	}

	for i := range want.Indexes {
		if !sameIndex(want.Indexes[i], got.Indexes[i]) {
			return report(want.Name.String()+" index "+want.Indexes[i].Name.String(),
				want.Indexes[i], got.Indexes[i])
		}
	}

	return ""
}

// Compared field by field because both carry a slice, which == cannot compare.
func sameConstraint(want, got catalog.Constraint) bool {
	return want.Name == got.Name && want.Kind == got.Kind &&
		want.Definition == got.Definition && sameNames(want.Columns, got.Columns)
}

func sameIndex(want, got catalog.Index) bool {
	return want.Name == got.Name && want.Unique == got.Unique && want.Primary == got.Primary &&
		want.Definition == got.Definition && sameNames(want.Columns, got.Columns)
}

func sameNames(want, got []catalog.Name) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] != got[i] {
			return false
		}
	}

	return true
}

func constraintNames(table catalog.Table) []string {
	found := make([]string, 0, len(table.Constraints))
	for _, constraint := range table.Constraints {
		found = append(found, constraint.Name.String())
	}

	return found
}

func indexNames(table catalog.Table) []string {
	found := make([]string, 0, len(table.Indexes))
	for _, index := range table.Indexes {
		found = append(found, index.Name.String())
	}

	return found
}

func report(what string, want, got any) string {
	return fmt.Sprintf("%s: %+v here, %+v there", what, want, got)
}

// The corpus is not just read without error: what it holds is what the model
// has to say it holds. This checks the shapes the phase is about, on the oldest
// version, where anything missing is missing everywhere.
func TestAColumnIsReadWithEverythingTheCatalogKnows(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])
	shapes := tableIn(t, schema, "column_shapes")

	if got := columnIn(t, shapes, "id").Identity; got != "always" {
		t.Errorf("the identity column reads as %q, want always", got)
	}
	if got := columnIn(t, shapes, "by_default").Identity; got != "by default" {
		t.Errorf("the by-default identity column reads as %q", got)
	}
	if got := columnIn(t, shapes, "generated_column").Generated; got != "stored" {
		t.Errorf("the generated column reads as %q, want stored", got)
	}
	if got := columnIn(t, shapes, "collated").Collation.String(); got != "C" {
		t.Errorf("the collated column reads its collation as %q, want C", got)
	}
	if !columnIn(t, shapes, "not_nullable").NotNull {
		t.Error("a NOT NULL column reads as nullable")
	}
	if columnIn(t, shapes, "nullable").NotNull {
		t.Error("a nullable column reads as NOT NULL")
	}
	if got := columnIn(t, shapes, "plain_default").Default; got == "" {
		t.Error("a column with a default reads as having none")
	}
	if got := columnIn(t, shapes, "nullable").Default; got != "" {
		t.Errorf("a column with no default reads as having %q", got)
	}
}

// The aliases fold. Every column of this table was declared with a spelling the
// server accepts as a synonym, and the model has to hold one spelling for each
// — otherwise a diff between this table and the same one written the long way
// reports a change on every column.
func TestTheAliasesOfATypeFoldToOneSpelling(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])
	aliases := tableIn(t, schema, "type_aliases")

	want := map[string]string{
		"a": "integer",
		"b": "integer",
		"c": "smallint",
		"d": "bigint",
		"e": "boolean",
		"f": "real",
		"g": "double precision",
		"h": "character varying(50)",
		"i": "numeric(10,2)",
		"j": "timestamp with time zone",
		"k": "timestamp without time zone",
		"l": "time with time zone",
		"m": "time without time zone",
		"n": "character(3)",
		"o": "integer",
	}

	for column, spelling := range want {
		if got := columnIn(t, aliases, column).Type.String(); got != spelling {
			t.Errorf("%s reads as %q, want %q", column, got, spelling)
		}
	}
}

// A type somebody made is not folded into a built-in, and it keeps the case it
// was created with.
func TestATypeOfTheirOwnSurvivesTheReading(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])
	shapes := tableIn(t, schema, "column_shapes")

	for column, want := range map[string]string{
		"typed":    "mood",
		"domained": "positive",
		"numbers":  "integer[]",
	} {
		if got := columnIn(t, shapes, column).Type.String(); got != want {
			t.Errorf("%s reads as %q, want %q", column, got, want)
		}
	}
}

// Names that need quoting come back as the identifier, not as the quoted form:
// what is stored is what the object is called, and the quotes belong to the
// writing.
func TestANameThatNeedsQuotingIsReadUnquoted(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])
	customer := tableIn(t, schema, "Customer")

	columnIn(t, customer, "Full Name")
	columnIn(t, customer, "endereço")
}

// A dropped column leaves its entry in pg_attribute so the row layout does not
// move. A reader that does not skip it puts a column nobody declared, with a
// name nobody can type, into the model — and into the DDL after it.
func TestADroppedColumnIsNotRead(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])
	holed := tableIn(t, schema, "with_a_hole")

	if len(holed.Columns) != 2 {
		t.Errorf("the table reads with %d columns, want the dropped one skipped: %v",
			len(holed.Columns), holed.Columns)
	}
	for _, column := range holed.Columns {
		if column.Name.String() == "gone" {
			t.Error("the dropped column is in the model")
		}
	}
}

// A partitioned table and a table with no columns are both legal, and both are
// the shape that finds a reader assuming otherwise.
func TestTheShapesThatBreakAnAssumption(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])

	if got := len(tableIn(t, schema, "empty_table").Columns); got != 0 {
		t.Errorf("a table with no columns reads with %d", got)
	}

	// The partitioned table and its partition are both tables here. How they
	// relate is not modelled yet; that they are not silently missing is what
	// this asserts.
	tableIn(t, schema, "measurements")
	tableIn(t, schema, "measurements_2026")

	// Inheritance likewise: both tables exist, and the child carries the
	// inherited columns as its own, which is what the catalog says.
	child := tableIn(t, schema, "child")
	columnIn(t, child, "common")
	columnIn(t, child, "extra")
}

// A schema that is not there is told apart from one that is empty, against a
// real server rather than only against a double.
func TestReadingASchemaTheServerDoesNotHave(t *testing.T) {
	t.Parallel()

	session, _ := openSession(t, testsupport.SupportedVersions[0])

	_, err := catalog.NewReader(session).Read(t.Context(), catalog.NewName("no_such_schema_here"))
	if !errors.Is(err, catalog.ErrSchemaNotFound) {
		t.Errorf("Read() = %v, want ErrSchemaNotFound", err)
	}
}

// Reading the same schema twice gives the same model. It is the property the
// diff rests on, and the first place it can be asserted against a real server:
// nothing about the reading may depend on the order the server happened to
// answer in.
func TestReadingTheSameSchemaTwiceGivesTheSameModel(t *testing.T) {
	t.Parallel()

	session, instance := openSession(t, testsupport.SupportedVersions[0])
	schema := testsupport.Corpus(t, instance)
	reader := catalog.NewReader(session)

	first, err := reader.Read(t.Context(), catalog.NewName(schema))
	if err != nil {
		t.Fatalf("the first read: %v", err)
	}

	second, err := reader.Read(t.Context(), catalog.NewName(schema))
	if err != nil {
		t.Fatalf("the second read: %v", err)
	}

	if difference := describe(first, second); difference != "" {
		t.Errorf("two readings of one schema differ: %s", difference)
	}
}

// constraintIn finds a constraint by name, failing the test when it is missing.
func constraintIn(t *testing.T, table catalog.Table, name string) catalog.Constraint {
	t.Helper()

	for _, constraint := range table.Constraints {
		if constraint.Name.String() == name {
			return constraint
		}
	}

	t.Fatalf("the table %s has no constraint %q; it has %v", table.Name, name, constraintNames(table))

	return catalog.Constraint{}
}

func indexIn(t *testing.T, table catalog.Table, name string) catalog.Index {
	t.Helper()

	for _, index := range table.Indexes {
		if index.Name.String() == name {
			return index
		}
	}

	t.Fatalf("the table %s has no index %q; it has %v", table.Name, name, indexNames(table))

	return catalog.Index{}
}

// The five kinds, read off a real server, with their keys in the order the key
// has.
func TestEveryKindOfConstraintIsRead(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])
	constrained := tableIn(t, schema, "constrained")

	for name, kind := range map[string]catalog.ConstraintKind{
		"constrained_pk":     catalog.ConstraintPrimaryKey,
		"constrained_unique": catalog.ConstraintUnique,
		"constrained_check":  catalog.ConstraintCheck,
	} {
		if got := constraintIn(t, constrained, name).Kind; got != kind {
			t.Errorf("%s reads as %q, want %q", name, got, kind)
		}
	}

	// The order of a key is part of it. The primary key is (first, second) and
	// the unique is (second, label): a reader that unnests without keeping the
	// ordinality gets one of them wrong.
	key := constraintIn(t, constrained, "constrained_pk").Columns
	if len(key) != 2 || key[0].String() != "first" || key[1].String() != "second" {
		t.Errorf("the primary key reads as %v, want [first second]", key)
	}

	unique := constraintIn(t, constrained, "constrained_unique").Columns
	if len(unique) != 2 || unique[0].String() != "second" || unique[1].String() != "label" {
		t.Errorf("the unique key reads as %v, want [second label]", unique)
	}

	booking := tableIn(t, schema, "booking")
	if len(booking.Constraints) == 0 {
		t.Fatal("the exclusion constraint was not read at all")
	}
	if got := booking.Constraints[0].Kind; got != catalog.ConstraintExclusion {
		t.Errorf("the exclusion constraint reads as %q", got)
	}
}

// Foreign keys, including the two shapes a dependency order has to survive: a
// table pointing at itself, and two pointing at each other.
func TestForeignKeysAreReadIncludingTheCircularOnes(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])

	self := tableIn(t, schema, "employee")
	found := false
	for _, constraint := range self.Constraints {
		if constraint.Kind == catalog.ConstraintForeignKey {
			found = true
		}
	}
	if !found {
		t.Errorf("the self-referencing foreign key was not read: %v", constraintNames(self))
	}

	toB := constraintIn(t, tableIn(t, schema, "circle_a"), "circle_a_to_b")
	toA := constraintIn(t, tableIn(t, schema, "circle_b"), "circle_b_to_a")

	if toB.Kind != catalog.ConstraintForeignKey || toA.Kind != catalog.ConstraintForeignKey {
		t.Errorf("the circular keys read as %q and %q", toB.Kind, toA.Kind)
	}
	// The referential action lives in the definition, and losing it would let a
	// sync recreate a key that deletes different rows.
	if !strings.Contains(toB.Definition, "ON DELETE SET NULL") {
		t.Errorf("circle_a_to_b reads as %q, want its ON DELETE", toB.Definition)
	}
	if !strings.Contains(toA.Definition, "ON DELETE CASCADE") {
		t.Errorf("circle_b_to_a reads as %q, want its ON DELETE", toA.Definition)
	}
}

// The indexes information_schema cannot see, which is one of the reasons the
// catalog is read directly.
func TestTheIndexesThatOnlyTheCatalogKnowsAreRead(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])
	indexed := tableIn(t, schema, "indexed")

	partial := indexIn(t, indexed, "indexed_partial")
	if !strings.Contains(partial.Definition, "WHERE") {
		t.Errorf("the partial index reads as %q, want its WHERE", partial.Definition)
	}

	// An index by expression names no column: there is no column number to
	// join, which is exactly why the definition is kept.
	expression := indexIn(t, indexed, "indexed_expression")
	if len(expression.Columns) != 0 {
		t.Errorf("the index by expression names %v", expression.Columns)
	}
	if !strings.Contains(expression.Definition, "lower") {
		t.Errorf("the index by expression reads as %q", expression.Definition)
	}

	// INCLUDE carries a column without ordering by it, and the key has to stop
	// where the key stops.
	include := indexIn(t, indexed, "indexed_include")
	if len(include.Columns) != 1 || include.Columns[0].String() != "status" {
		t.Errorf("the INCLUDE index has key %v, want [status]", include.Columns)
	}
	if !strings.Contains(include.Definition, "INCLUDE") {
		t.Errorf("the INCLUDE index reads as %q, want its payload", include.Definition)
	}

	if !indexIn(t, indexed, "indexed_unique").Unique {
		t.Error("the unique index reads as not unique")
	}
	if !strings.Contains(indexIn(t, indexed, "indexed_descending").Definition, "DESC") {
		t.Error("the descending index lost its direction")
	}
}

// An index that exists only because a constraint does is the constraint's, and
// listing it as well would put one object in the model twice — and make the DDL
// phase emit a constraint and then an index the constraint already created.
func TestAnIndexBackingAConstraintIsNotListedTwice(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])
	constrained := tableIn(t, schema, "constrained")

	for _, index := range constrained.Indexes {
		if index.Name.String() == "constrained_pk" || index.Name.String() == "constrained_unique" {
			t.Errorf("%s is listed as an index as well as a constraint", index.Name)
		}
	}

	// And an index nobody declared a constraint for is still there.
	indexIn(t, tableIn(t, schema, "indexed"), "indexed_unique")
}

// The divergence the version matrix exists to find, asserted rather than
// assumed.
//
// PostgreSQL 18 began cataloguing NOT NULL as a constraint of its own, one row
// per column, where every earlier version records it only as attnotnull.
// Reading those rows would give the same schema two extra constraints per
// column on 18 and none on 17 — a diff between two servers reporting changes
// nobody made. This checks the model has none of them anywhere, on every
// version, so the filter that excludes them cannot be removed quietly.
func TestNotNullIsNeverReadAsAConstraint(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			schema := readCorpus(t, version)

			for _, table := range schema.Tables {
				for _, constraint := range table.Constraints {
					if constraint.Kind == "n" || strings.HasPrefix(constraint.Definition, "NOT NULL") {
						t.Errorf("PostgreSQL %s: %s.%s reads NOT NULL as a constraint (%q)",
							version, table.Name, constraint.Name, constraint.Definition)
					}
				}
			}

			// And the column still says so, which is where every version agrees
			// it lives.
			if !columnIn(t, tableIn(t, schema, "constrained"), "first").NotNull {
				t.Errorf("PostgreSQL %s: the column lost its NOT NULL", version)
			}
		})
	}
}

// sequenceIn finds a sequence by name, failing the test when it is not there.
func sequenceIn(t *testing.T, schema catalog.Schema, name string) catalog.Sequence {
	t.Helper()

	for _, sequence := range schema.Sequences {
		if sequence.Name.String() == name {
			return sequence
		}
	}

	t.Fatalf("the schema has no sequence %q; it has %v", name, sequenceNames(schema))

	return catalog.Sequence{}
}

// viewIn finds a view by name, failing the test when it is not there.
func viewIn(t *testing.T, schema catalog.Schema, name string) catalog.View {
	t.Helper()

	for _, view := range schema.Views {
		if view.Name.String() == name {
			return view
		}
	}

	t.Fatalf("the schema has no view %q; it has %v", name, viewNames(schema))

	return catalog.View{}
}

// A sequence is read with every number that decides what it hands out, off a
// real server. Defaulting any of them here would produce a copy that counts
// differently from the original — a data fault produced by a copy of the
// structure, and one nothing in the diff would have shown.
func TestASequenceIsReadWithWhatItHandsOut(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])
	counter := sequenceIn(t, schema, "standalone_counter")

	if got := counter.Type.String(); got != "smallint" {
		t.Errorf("the sequence counts in %q, want smallint", got)
	}
	if counter.Start != 100 || counter.Increment != 5 {
		t.Errorf("it starts at %d and steps by %d, want 100 and 5", counter.Start, counter.Increment)
	}
	if counter.Min != 10 || counter.Max != 30000 {
		t.Errorf("its bounds read as %d..%d, want 10..30000", counter.Min, counter.Max)
	}
	if counter.Cache != 20 || !counter.Cycle {
		t.Errorf("it caches %d and cycles %v, want 20 and true", counter.Cache, counter.Cycle)
	}

	// A sequence nobody owns is the ordinary shape of a counter somebody made,
	// and it must not acquire an owner from a join that found the wrong row.
	if counter.OwnedBy.Valid() {
		t.Errorf("a standalone sequence reads as owned by %+v", counter.OwnedBy)
	}
}

// Every way a column can own a sequence, read off a real server. Losing the
// link leaves the copy at the other end with a sequence nothing owns and a
// column whose default points at it anyway.
func TestASequenceIsReadWithTheColumnThatOwnsIt(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])

	// Declared with OWNED BY, which is the explicit form.
	owned := sequenceIn(t, schema, "counted_id_seq").OwnedBy
	if owned.Table.String() != "counted" || owned.Column.String() != "id" {
		t.Errorf("the declared sequence reads as owned by %+v, want counted.id", owned)
	}

	// Made by serial, which arranges the same link behind the scenes.
	serial := sequenceIn(t, schema, "type_aliases_o_seq").OwnedBy
	if serial.Table.String() != "type_aliases" || serial.Column.String() != "o" {
		t.Errorf("the serial sequence reads as owned by %+v, want type_aliases.o", serial)
	}
}

// The sequence behind an identity column is read, and it is read as the
// column's.
//
// It is the one object here that could reasonably have been left out — nobody
// declared it, and the index backing a constraint is left out for what looks
// like the same reason. It is not the same reason. That index carries nothing
// the constraint does not already say; this sequence carries its start, its
// step and its name, and the column says none of them. Dropping the row would
// lose all of that, and a copy of an identity declared START WITH 5 would count
// from one.
func TestTheSequenceBehindAnIdentityColumnIsReadAsTheColumnsOwn(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])

	for name, column := range map[string]string{
		"column_shapes_id_seq":         "id",
		"column_shapes_by_default_seq": "by_default",
	} {
		owned := sequenceIn(t, schema, name).OwnedBy
		if owned.Table.String() != "column_shapes" || owned.Column.String() != column {
			t.Errorf("%s reads as owned by %+v, want column_shapes.%s", name, owned, column)
		}
	}

	// And the column still says it is an identity column, which is what tells
	// the DDL writer not to emit a CREATE SEQUENCE beside the clause that
	// already creates one.
	if got := columnIn(t, tableIn(t, schema, "column_shapes"), "id").Identity; got != "always" {
		t.Errorf("the identity column reads as %q", got)
	}
}

// Views come back with the query behind them, rendered from the parse tree
// rather than as anybody typed it.
func TestTheViewsOfTheCorpusAreReadWithTheirQuery(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])

	if got := viewIn(t, schema, "active_orders").Definition; !strings.Contains(got, "indexed") {
		t.Errorf("the view reads as %q, want the table it selects from", got)
	}

	// A view standing on another view is the dependency the ordering phase will
	// have to work out; that both are read at all is what this asserts.
	standing := viewIn(t, schema, "active_domains").Definition
	if !strings.Contains(standing, "active_orders") {
		t.Errorf("the view over a view reads as %q, want the view it selects from", standing)
	}

	// A query that refers to itself through a recursive CTE resolves inside the
	// query. A reader that mistook the self-reference for a dependency on the
	// view would find a cycle that is not there.
	recursive := viewIn(t, schema, "reporting_line").Definition
	if !strings.Contains(recursive, "RECURSIVE") {
		t.Errorf("the recursive view reads as %q, want its WITH RECURSIVE", recursive)
	}

	// The definition is what goes after AS: no leading space, no terminator.
	// Keeping them would make the same query read from a server and written by
	// hand compare unequal.
	for _, view := range schema.Views {
		if strings.HasSuffix(view.Definition, ";") || view.Definition != strings.TrimSpace(view.Definition) {
			t.Errorf("the definition of %s is %q, want it without the punctuation", view.Name, view.Definition)
		}
	}
}

// WITH CHECK OPTION is stored beside the view rather than inside its query, so
// pg_get_viewdef never mentions it. A reader that asks only for the definition
// drops the clause that decides whether a write through the view is refused,
// and the copy at the other end accepts rows the original rejects.
func TestAViewKeepsItsCheckOption(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])

	if got := viewIn(t, schema, "checked_orders").CheckOption; got != "cascaded" {
		t.Errorf("the checked view reads its check option as %q, want cascaded", got)
	}
	if got := viewIn(t, schema, "active_orders").CheckOption; got != "" {
		t.Errorf("a view with no check option reads as %q", got)
	}
}

// A view is not a table and a sequence is not a table. They are all rows of
// pg_class, and a filter that let one through as another would put an object in
// the model that no DDL of that kind can create.
func TestViewsAndSequencesAreNotReadAsTables(t *testing.T) {
	t.Parallel()

	schema := readCorpus(t, testsupport.SupportedVersions[0])

	elsewhere := map[string]bool{}
	for _, view := range schema.Views {
		elsewhere[view.Name.String()] = true
	}
	for _, sequence := range schema.Sequences {
		elsewhere[sequence.Name.String()] = true
	}

	for _, table := range schema.Tables {
		if elsewhere[table.Name.String()] {
			t.Errorf("%s is listed as a table as well", table.Name)
		}
	}
}

// The divergence the matrix found, pinned so that it is a known gap and not a
// forgotten one.
//
// PostgreSQL 16 changed how a view is rendered back. Up to 15 a column is
// qualified with the relation it came from and from 16 the qualification is
// left out where nothing is ambiguous, so the same view read on two servers
// gives this model two spellings of one query — and a diff between a server on
// 15 and its copy on 17 would report every view as changed.
//
// The matrix cannot assert equality of the text until that is canonicalised,
// and canonicalising it is not a substring away: telling a qualification that
// can be dropped from one that carries meaning is a question about the query
// rather than about its characters. Dropping every qualification would make
// SELECT a.note and SELECT b.note the same text, which trades this false
// positive for a false negative — a real change the sync would stop applying.
// ADR-0012 records why it is left as it is. So this asserts the difference
// instead, in the exact shape it has. The day it is closed this test fails, which is the
// point — it is what makes the matrix comparing everything about a view again a
// decision somebody makes rather than something nobody remembers to revisit.
func TestAViewDefinitionStillDependsOnTheServerVersion(t *testing.T) {
	t.Parallel()

	// Named rather than taken from the ends of the matrix: the change landed
	// between these two, and pinning them keeps the test about what it found.
	const (
		qualifies      = "15"
		doesNotAnyMore = "16"
	)

	before := viewIn(t, readCorpus(t, qualifies), "active_domains").Definition
	after := viewIn(t, readCorpus(t, doesNotAnyMore), "active_domains").Definition

	if before == after {
		t.Fatalf("PostgreSQL %s and %s now render a view alike (%q); the matrix can compare "+
			"the text of a definition again", qualifies, doesNotAnyMore, before)
	}
	if !strings.Contains(before, "active_orders.email") {
		t.Errorf("PostgreSQL %s renders %q, want the column qualified", qualifies, before)
	}
	if strings.Contains(after, "active_orders.email") {
		t.Errorf("PostgreSQL %s renders %q, want the qualification left out",
			doesNotAnyMore, after)
	}
}

// Two schemas with the same structure under different names read as the same
// model.
//
// This is the property the diff rests on, and it is the one this package exists
// to make true: comparing an environment against its copy means reading two
// schemas that are structurally identical and called different things, and
// every difference the reader invents there is a change the diff reports that
// nobody made. The fixture builds a schema of its own per call, so two calls
// are exactly that pair.
func TestTwoSchemasWithOneStructureReadTheSame(t *testing.T) {
	t.Parallel()

	session, instance := openSession(t, testsupport.SupportedVersions[0])
	reader := catalog.NewReader(session)

	first, second := testsupport.Corpus(t, instance), testsupport.Corpus(t, instance)

	one, err := reader.Read(t.Context(), catalog.NewName(first))
	if err != nil {
		t.Fatalf("reading %s: %v", first, err)
	}

	other, err := reader.Read(t.Context(), catalog.NewName(second))
	if err != nil {
		t.Fatalf("reading %s: %v", second, err)
	}

	// The names are the one thing that legitimately differs, and describe does
	// not look at them.
	if difference := describe(one, other); difference != "" {
		t.Errorf("two copies of one structure read differently: %s", difference)
	}
}

// The same schema read over two connections gives the same model. Nothing about
// a reading may depend on which connection asked — a model that did would make
// the diff's answer depend on which pool slot it happened to get.
func TestTheSameSchemaReadOverTwoConnectionsIsTheSameModel(t *testing.T) {
	t.Parallel()

	session, instance := openSession(t, testsupport.SupportedVersions[0])
	schema := testsupport.Corpus(t, instance)

	other, _ := openSession(t, testsupport.SupportedVersions[0])

	here, err := catalog.NewReader(session).Read(t.Context(), catalog.NewName(schema))
	if err != nil {
		t.Fatalf("reading over the first connection: %v", err)
	}

	there, err := catalog.NewReader(other).Read(t.Context(), catalog.NewName(schema))
	if err != nil {
		t.Fatalf("reading over the second connection: %v", err)
	}

	if difference := describe(here, there); difference != "" {
		t.Errorf("two connections read one schema differently: %s", difference)
	}
}

// The read leaves the session exactly as it found it.
//
// The path it points at the schema is state the reader borrows and does not
// own. A connection handed back to the pool still pointing at whatever schema
// the last read wanted resolves the next caller's unqualified names against it,
// which is a bug that surfaces far away from here and looks like anything but
// this.
func TestAReadLeavesTheSearchPathAsItFoundIt(t *testing.T) {
	t.Parallel()

	session, instance := openSession(t, testsupport.SupportedVersions[0])
	schema := testsupport.Corpus(t, instance)

	before := searchPath(t, session)

	if _, err := catalog.NewReader(session).Read(t.Context(), catalog.NewName(schema)); err != nil {
		t.Fatalf("Read() = %v", err)
	}

	if after := searchPath(t, session); after != before {
		t.Errorf("the session is on %q after the read, was on %q", after, before)
	}

	// And a read that fails part way through puts it back too, which is the
	// case a deferred restore exists for.
	if _, err := catalog.NewReader(session).Read(t.Context(), catalog.NewName("no_such_schema")); err == nil {
		t.Fatal("reading a schema that is not there = nil")
	}
	if after := searchPath(t, session); after != before {
		t.Errorf("the session is on %q after a failed read, was on %q", after, before)
	}
}

func searchPath(t *testing.T, session driver.Session) string {
	t.Helper()

	rows := session.Query(t.Context(), `SELECT pg_catalog.current_setting('search_path')`)
	defer rows.Close()

	if !rows.Next() {
		t.Fatalf("asking for the search path: %v", rows.Err())
	}

	var path string
	if err := rows.Scan(&path); err != nil {
		t.Fatalf("reading the search path: %v", err)
	}

	return path
}

// Nothing the model holds as text names the schema it was read from.
//
// It is the property the whole canonicalisation rests on, asserted directly
// rather than only through two readings agreeing: a default, a constraint, an
// index or a view carrying the schema is a difference between an environment
// and its copy on every object that has one.
func TestNothingReadCarriesTheNameOfItsSchema(t *testing.T) {
	t.Parallel()

	session, instance := openSession(t, testsupport.SupportedVersions[0])
	schema := testsupport.Corpus(t, instance)

	read, err := catalog.NewReader(session).Read(t.Context(), catalog.NewName(schema))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	mentions := func(what, text string) {
		t.Helper()

		if strings.Contains(text, schema) {
			t.Errorf("%s names its schema: %q", what, text)
		}
	}

	for _, table := range read.Tables {
		for _, column := range table.Columns {
			mentions("the default of "+table.Name.String()+"."+column.Name.String(), column.Default)
			mentions("the type of "+table.Name.String()+"."+column.Name.String(), column.Type.String())
			mentions("the generated expression of "+column.Name.String(), column.Generated)
		}
		for _, constraint := range table.Constraints {
			mentions("the constraint "+constraint.Name.String(), constraint.Definition)
		}
		for _, index := range table.Indexes {
			mentions("the index "+index.Name.String(), index.Definition)
		}
	}

	for _, view := range read.Views {
		mentions("the view "+view.Name.String(), view.Definition)
	}
}

// A column that declares no collation still reads one.
//
// A collatable column always has a collation — the database's, named default —
// and leaving it empty for the common case would make every column that
// declares one differ from every column that does not, on two schemas that
// declare exactly the same thing. A column of a type that cannot be collated
// has none, and inventing one for it would be the same mistake mirrored.
func TestACollationIsAlwaysExplicit(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			schema := readCorpus(t, version)
			shapes := tableIn(t, schema, "column_shapes")

			for _, column := range []string{"nullable", "not_nullable", "cast_default"} {
				if got := columnIn(t, shapes, column).Collation.String(); got != "default" {
					t.Errorf("%s reads its collation as %q, want default", column, got)
				}
			}

			if got := columnIn(t, shapes, "collated").Collation.String(); got != "C" {
				t.Errorf("the declared collation reads as %q, want C", got)
			}
			if got := columnIn(t, shapes, "plain_default").Collation.String(); got != "" {
				t.Errorf("an integer column reads a collation of %q, want none", got)
			}
		})
	}
}

// A schema that shadows the functions and operators the reader uses does not
// get to choose what the reader reads.
//
// This is CVE-2018-1058 applied to this package. The reader points the search
// path at the schema it is reading — which it must, because that is what makes
// the expressions the server renders independent of the schema's name — and
// from that moment anything declared in that schema is on the path. A call left
// unqualified there is a call the schema's owner decides.
//
// pg_catalog being implicitly first does not save it, and that is the part
// worth stating because it is the part that looks like it should. Path order
// only settles two candidates with identical signatures. The corpus declares
// unnest(smallint[]) and unnest(int2vector) against the catalog's
// unnest(anyarray), and array_agg(name) against array_agg(anynonarray): exact
// matches against polymorphic ones, which win from anywhere on the path.
//
// The same holds for operators, and there the exposure is wider than it looks:
// pg_catalog has an exact operator for a "char" against an untyped literal, and
// none for an oid against a regclass or an integer. Those two resolve by
// coercion, and a coercion loses to an exact match. The corpus declares all
// three, so a comparison that loses its qualification reads a schema with
// nothing in it.
//
// The values below come from those calls. A key read through a shadowed unnest
// comes back as the single column 666 — which is not a column of anything — and
// a parent list read through a shadowed array_agg comes back as "hijacked".
// Asserting the real values is asserting that the server used the catalog's own
// functions and operators.
func TestASchemaCannotHijackWhatTheReaderCalls(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			schema := readCorpus(t, version)

			// unnest, through the key of a constraint. The order matters as
			// much as the names, and both come from the ordinality the
			// shadowed function does not produce.
			key := constraintIn(t, tableIn(t, schema, "constrained"), "constrained_pk")
			if got := columnNames(key.Columns); !reflect.DeepEqual(got, []string{"first", "second"}) {
				t.Errorf("the key of the primary key reads as %v, want [first second]", got)
			}

			// unnest again, through the columns of an index — a different
			// argument type, int2vector, and so a different shadowing function.
			index := indexIn(t, tableIn(t, schema, "indexed"), "indexed_unique")
			if got := columnNames(index.Columns); !reflect.DeepEqual(got, []string{"email", "status"}) {
				t.Errorf("the columns of the index read as %v, want [email status]", got)
			}

			// array_agg, through the parents of an inherited table.
			child := tableIn(t, schema, "child")
			if got := columnNames(child.Inherits); !reflect.DeepEqual(got, []string{"parent"}) {
				t.Errorf("the parents of the child read as %v, want [parent]", got)
			}

			// The operators. Each of the three shadowed pairs sits under a
			// different read, and each answers false, so a hijack empties the
			// list rather than bending it.
			//
			// oid against a regclass literal is what the dependency graph and
			// the sequences are found by; oid against an integer is what tells
			// a constraint somebody declared from one the server made; "char"
			// against an untyped literal is every relkind and contype filter
			// there is.
			if len(schema.Dependencies) == 0 {
				t.Error("the schema reads with no dependencies at all, which is what a hijacked oid = regclass answers")
			}
			if len(schema.Sequences) == 0 {
				t.Error("the schema reads with no sequences at all")
			}
			if len(tableIn(t, schema, "constrained").Constraints) == 0 {
				t.Error("the table reads with no constraints at all, which is what a hijacked oid = integer answers")
			}
			if len(schema.Tables) == 0 || len(schema.Views) == 0 {
				t.Error("the schema reads with no tables or no views, which is what a hijacked \"char\" = text answers")
			}
		})
	}
}

func columnNames(names []catalog.Name) []string {
	found := make([]string, 0, len(names))
	for _, name := range names {
		found = append(found, name.String())
	}

	return found
}

// A unique index somebody made by hand stays in the model even when a foreign
// key points at it.
//
// A foreign key fills conindid with the index of the table it references, so a
// filter that read conindid as "this index belongs to a constraint" excluded an
// index no constraint owned. The loss was silent in the model and loud later:
// the generated script died on the ALTER TABLE that adds the key, because the
// unique index that key requires had never been created.
func TestAUniqueIndexAForeignKeyPointsAtIsStillAnIndex(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			schema := readCorpus(t, version)

			index := indexIn(t, tableIn(t, schema, "referenced"), "referenced_code")
			if !index.Unique || index.Primary {
				t.Errorf("the index reads as unique=%v primary=%v, want a plain unique index",
					index.Unique, index.Primary)
			}

			// And the constraint it is not part of is still read as a
			// constraint of the other table, so nothing was traded for it.
			constraintIn(t, tableIn(t, schema, "referring"), "referring_code_fkey")
		})
	}
}

// A bpchar column keeps the length it has, and keeps not having one.
//
// The two are different types wearing one word. bpchar(3) is exactly
// character(3); a bare bpchar is the unlimited blank-padded type, and a bare
// character is character(1). Folding the bare spelling into character wrote a
// column of a single character where the original held any length — a copy of a
// structure losing data, invisible until a row came back truncated.
//
// Read off a real server on every version, because what makes this the right
// answer is what format_type renders, not what the alias table was told.
func TestABpcharColumnKeepsWhetherItHasALength(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			aliases := tableIn(t, readCorpus(t, version), "type_aliases")

			if got := columnIn(t, aliases, "p").Type.String(); got != "bpchar" {
				t.Errorf("a bpchar with no length reads as %q, want bpchar", got)
			}
			if got := columnIn(t, aliases, "q").Type.String(); got != "character(3)" {
				t.Errorf("a bpchar(3) reads as %q, want character(3)", got)
			}
		})
	}
}

// What a table is made of is not all a table is: whether its writes are logged,
// and the storage parameters somebody set on it.
//
// Both change how the copy behaves rather than how it looks. An unlogged table
// recreated as an ordinary one has durability the original never had and the
// write cost that comes with it; an ordinary one recreated as unlogged throws
// away rows the first time the server stops badly. fillfactor and the autovacuum
// thresholds are what somebody tuned for a reason nobody wrote down.
func TestATableIsReadWithHowItIsStored(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			schema := readCorpus(t, version)

			if !tableIn(t, schema, "scratch").Unlogged {
				t.Error("the unlogged table reads as logged")
			}
			if tableIn(t, schema, "indexed").Unlogged {
				t.Error("an ordinary table reads as unlogged")
			}

			tuned := tableIn(t, schema, "tuned").Options
			want := []string{"autovacuum_vacuum_scale_factor=0.05", "fillfactor=70"}
			if !slices.Equal(tuned, want) {
				t.Errorf("the tuned table reads with the parameters %v, want %v", tuned, want)
			}
			if got := tableIn(t, schema, "indexed").Options; len(got) != 0 {
				t.Errorf("a table nobody tuned reads with the parameters %v", got)
			}
		})
	}
}

// A view keeps its security barrier, which decides what a cheap function is
// allowed to see.
//
// A view declared with one refuses to let a function chosen for being cheap run
// against rows the view was meant to hide. A copy made without it answers
// questions the original refused — a change of security posture produced by a
// copy of a structure, which is exactly the kind of difference a model that
// loses it cannot report.
//
// The check option is kept out of this list, because it has a field of its own:
// carrying it in both would have the writer emit it twice and the diff report
// one change as two.
func TestAViewKeepsItsSecurityBarrier(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			schema := readCorpus(t, version)

			guarded := viewIn(t, schema, "guarded").Options
			if !slices.Equal(guarded, []string{"security_barrier=true"}) {
				t.Errorf("the guarded view reads with the parameters %v", guarded)
			}

			// The one that carries a check option carries it as a check option
			// and not as a parameter.
			checked := viewIn(t, schema, "checked_orders")
			if len(checked.Options) != 0 {
				t.Errorf("the check option appears among the parameters %v", checked.Options)
			}
			if checked.CheckOption != "cascaded" {
				t.Errorf("the check option reads as %q, want cascaded", checked.CheckOption)
			}
		})
	}
}

// An index declared once on a partitioned table is read once.
//
// PostgreSQL clones such an index onto every partition, so a reader that takes
// pg_index at face value finds one index per partition for an index somebody
// wrote a single time — and the writer would emit each of them against a table
// whose own DDL already creates it.
//
// The clone is told from the declaration by relispartition on the index itself,
// not on the table it is on: both live in the schema and both look like indexes
// of a table this reader lists.
func TestAnIndexOnAPartitionedTableIsReadOnce(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			schema := readCorpus(t, version)

			// Declared on the parent, so it belongs to the parent and nowhere
			// else.
			indexIn(t, tableIn(t, schema, "measurements"), "measurements_taken")

			partition := tableIn(t, schema, "measurements_2026")
			if len(partition.Indexes) != 0 {
				t.Errorf("the partition reads with the indexes %v, want the clones left out",
					indexNames(partition))
			}
		})
	}
}

// A schema read while somebody else is changing it comes back whole.
//
// This is the property the snapshot exists for, against a real server rather
// than a double. A table is dropped from another connection in the middle of the
// read — between the statement that lists the tables and the ones that fill
// them — and the reading must either hold the table as it was or fail. What it
// must not do is hold the table with nothing in it, which is what eleven
// statements against eleven views of the database produce, and which no diff can
// tell from a table that really did lose everything.
//
// The drop runs on a connection of its own, because the reader's is inside the
// transaction being tested. It is committed before the read is allowed to go on,
// so this is not a race the test hopes to win: the table is certainly gone from
// every view taken after it.
//
// What the snapshot buys is measured rather than assumed: a catalog scan inside
// REPEATABLE READ keeps answering from the view the transaction opened with,
// which was confirmed against a server. The renderers — pg_get_constraintdef and
// its family — do not, because they read the catalog through a snapshot of their
// own, so a table with a constraint on it makes the read fail instead. Failing
// is a fine answer. Coming back hollow is not, and that is what this pins.
func TestASchemaIsReadWholeWhileSomebodyElseChangesIt(t *testing.T) {
	t.Parallel()

	session, instance := openSession(t, testsupport.SupportedVersions[0])
	corpus := testsupport.Corpus(t, instance)

	// A table of its own, so dropping it cannot disturb the rest of the corpus,
	// and with nothing on it that the server renders: a constraint or an index
	// would send pg_get_constraintdef after an object that is gone, and those
	// functions read the catalog through a snapshot of their own rather than
	// the transaction's. That is a failure rather than a hollow table, which is
	// an acceptable answer — but the property worth pinning is the other one,
	// so this table is shaped to reach it.
	instance.Exec(t, "CREATE TABLE "+corpus+".doomed (id integer, label text)")

	reader := catalog.NewReader(&watched{
		Session: session,
		// After the tables are listed and before the columns are read, which is
		// the window that produced a table with no columns.
		after: "pg_class",
		then:  func() { instance.Exec(t, "DROP TABLE "+corpus+".doomed") },
	})

	read, err := reader.Read(t.Context(), catalog.NewName(corpus))
	if err != nil {
		t.Fatalf("reading %s: %v", corpus, err)
	}

	doomed := tableIn(t, read, "doomed")
	if len(doomed.Columns) != 2 {
		t.Errorf("the table dropped mid-read came back with %d columns, want the two it had"+
			" when the reading began", len(doomed.Columns))
	}
}

// watched runs something once, after the first query that reads a given catalog
// table, so a test can put a change exactly in the window it is about.
type watched struct {
	driver.Session

	after string
	then  func()
	once  sync.Once
}

func (w *watched) Query(ctx context.Context, sql string, args ...any) driver.Rows {
	rows := w.Session.Query(ctx, sql, args...)

	if strings.Contains(sql, "FROM pg_catalog."+w.after) {
		w.once.Do(w.then)
	}

	return rows
}

// A table that restricts which rows are visible says so.
//
// The policies themselves are not compared by this version, and that is exactly
// why the fact has to be read: a copy of such a table made without its row
// security shows every row the original hid, and nothing about the copy would
// say a control had been dropped on the way. It is the same rule as
// partitioning and the stakes are higher — the DDL writer declines the table
// rather than producing one that reveals more than what it was copied from.
func TestATableSaysWhetherItRestrictsWhichRowsAreVisible(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			schema := readCorpus(t, version)

			patient := tableIn(t, schema, "patient")
			if !patient.RowSecurity || !patient.Forced {
				t.Errorf("the guarded table reads as security=%v forced=%v, want both",
					patient.RowSecurity, patient.Forced)
			}

			if ordinary := tableIn(t, schema, "indexed"); ordinary.RowSecurity || ordinary.Forced {
				t.Errorf("an ordinary table reads as security=%v forced=%v",
					ordinary.RowSecurity, ordinary.Forced)
			}
		})
	}
}
