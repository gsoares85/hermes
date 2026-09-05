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
			table.Columns[j].Default = strings.ReplaceAll(
				table.Columns[j].Default, name+".", "{schema}.")
		}
		anonymised.Tables[i] = table
	}

	return anonymised
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

	return ""
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
