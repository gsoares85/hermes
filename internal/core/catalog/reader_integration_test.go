//go:build integration

package catalog_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
	"github.com/gsoares85/hermes/internal/core/conn"
	"github.com/gsoares85/hermes/internal/driver"
	"github.com/gsoares85/hermes/internal/driver/postgres"
	"github.com/gsoares85/hermes/internal/testsupport"
)

// openSession brings up a server of the given version and checks out a session
// against it.
func openSession(t *testing.T, version string) (driver.Session, *testsupport.Instance) {
	t.Helper()

	instance := testsupport.SharedPostgres(t, version)

	config, err := conn.ParseURI(instance.DSN)
	if err != nil {
		t.Fatalf("parsing the DSN: %v", err)
	}

	pool, err := postgres.New().Open(t.Context(), config.Target())
	if err != nil {
		t.Fatalf("opening a pool: %v", err)
	}
	t.Cleanup(pool.Close)

	session, err := pool.Session(t.Context())
	if err != nil {
		t.Fatalf("checking out a session: %v", err)
	}
	t.Cleanup(session.Close)

	return session, instance
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

// anonymise replaces the name of the corpus schema wherever it appears, so that
// two readings can be compared for structure.
//
// The fixture numbers its schema so that two calls inside one container cannot
// collide, which means every version reads a schema under a different name.
// That difference is the test's own doing and has to come out of the
// comparison; nothing else does.
//
// It has to reach inside the default of a column, because a serial declares one
// that names its sequence — nextval('corpus_8.type_aliases_o_seq'::regclass) —
// and the schema is in there. That the model still carries a schema-qualified
// default at all is a real gap, and it is the one the canonicalisation phase
// exists to close: two structurally identical schemas under different names
// have defaults that differ only by the name, and a diff would report every
// serial column as changed. Neutralising exactly the known name here keeps this
// test about versions, which is what it is for, and leaves that gap to be
// closed where it belongs rather than papered over by a broad substitution.
func anonymise(schema catalog.Schema) catalog.Schema {
	name := schema.Name.String()

	anonymised := schema
	anonymised.Name = catalog.Name{}
	anonymised.Tables = make([]catalog.Table, len(schema.Tables))

	for i, table := range schema.Tables {
		table.Columns = append([]catalog.Column(nil), table.Columns...)
		for j := range table.Columns {
			table.Columns[j].Default = anonymiseText(table.Columns[j].Default, name)
		}

		table.Constraints = append([]catalog.Constraint(nil), table.Constraints...)
		for j := range table.Constraints {
			table.Constraints[j].Definition = anonymiseText(table.Constraints[j].Definition, name)
		}

		table.Indexes = append([]catalog.Index(nil), table.Indexes...)
		for j := range table.Indexes {
			table.Indexes[j].Definition = anonymiseText(table.Indexes[j].Definition, name)
		}

		anonymised.Tables[i] = table
	}

	return anonymised
}

func anonymiseText(text, schema string) string {
	return strings.ReplaceAll(text, schema+".", "{schema}.")
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
// This is the assertion the whole task is arranged around, at the size this
// phase can make it: the reader is not allowed to answer differently because
// the server is older or newer. A difference here is a false positive waiting
// in the diff, and finding it now costs a test run rather than a user's trust.
func TestTheCorpusReadsTheSameOnEveryVersion(t *testing.T) {
	t.Parallel()

	var first catalog.Schema

	firstVersion := testsupport.SupportedVersions[0]

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			read := anonymise(readCorpus(t, version))

			if version == firstVersion {
				first = read

				return
			}

			if difference := describe(first, read); difference != "" {
				t.Errorf("PostgreSQL %s reads the corpus differently from %s: %s",
					version, firstVersion, difference)
			}
		})
	}
}

// describe answers what differs between two readings, in the first place they
// differ, so that a failure names the column rather than printing two models.
func describe(want, got catalog.Schema) string {
	if len(want.Tables) != len(got.Tables) {
		return report("tables", names(want), names(got))
	}

	for i := range want.Tables {
		if difference := describeTable(want.Tables[i], got.Tables[i]); difference != "" {
			return difference
		}
	}

	return ""
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

	return describeIndexes(want, got)
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
