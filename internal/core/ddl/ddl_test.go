package ddl_test

import (
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
	"github.com/gsoares85/hermes/internal/core/ddl"
)

func name(text string) catalog.Name { return catalog.NewName(text) }

func table(text string) catalog.Object {
	return catalog.Object{Kind: catalog.ObjectTable, Name: name(text)}
}

func view(text string) catalog.Object {
	return catalog.Object{Kind: catalog.ObjectView, Name: name(text)}
}

// only fails unless the script is exactly one statement, and answers it.
func only(t *testing.T, script ddl.Script) string {
	t.Helper()

	if len(script.Statements) != 1 {
		t.Fatalf("the script holds %d statements, want one: %v", len(script.Statements), script.Statements)
	}

	return script.Statements[0]
}

// mustWrite fails unless the script says the given text somewhere.
func mustWrite(t *testing.T, script ddl.Script, want string) {
	t.Helper()

	if !strings.Contains(script.String(), want) {
		t.Errorf("the script does not write %q:\n%s", want, script.String())
	}
}

// mustNotWrite fails when the script says the given text anywhere.
func mustNotWrite(t *testing.T, script ddl.Script, unwanted string) {
	t.Helper()

	if strings.Contains(script.String(), unwanted) {
		t.Errorf("the script writes %q and should not:\n%s", unwanted, script.String())
	}
}

// A table with its columns, which is the statement everything else hangs off.
func TestATableIsWrittenWithItsColumnsInOrder(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name: name("sales"),
		Tables: []catalog.Table{{
			Name: name("orders"),
			Columns: []catalog.Column{
				{Name: name("id"), Position: 1, Type: catalog.NewTypeName("integer"), NotNull: true},
				{Name: name("note"), Position: 2, Type: catalog.NewTypeName("text")},
			},
		}},
	}

	want := "CREATE TABLE \"orders\" (\n    \"id\" integer NOT NULL,\n    \"note\" text\n)"
	if got := only(t, ddl.Of(schema)); got != want {
		t.Errorf("the table is written as\n%s\nwant\n%s", got, want)
	}
}

// A table with no columns is legal, and it is the shape that finds a writer
// assuming there is always something between the parentheses.
func TestATableWithNoColumnsIsStillATable(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name:   name("sales"),
		Tables: []catalog.Table{{Name: name("empty_table")}},
	}

	if got := only(t, ddl.Of(schema)); got != `CREATE TABLE "empty_table" ()` {
		t.Errorf("the empty table is written as %q", got)
	}
}

// Every identifier is quoted, and a quote inside one is doubled.
//
// The doubling is the whole of it: a name written without it ends the
// identifier early and the rest of the name is parsed as SQL, which is an
// injection through an object name. The quoting is unconditional because the
// list of words that would need it changes between server versions.
func TestEveryNameIsQuotedAndAQuoteInsideOneIsDoubled(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name: name("sales"),
		Tables: []catalog.Table{{
			Name: name(`we"ird`),
			Columns: []catalog.Column{
				{Name: name("Full Name"), Position: 1, Type: catalog.NewTypeName("text")},
			},
		}},
	}

	statement := only(t, ddl.Of(schema))
	if !strings.Contains(statement, `CREATE TABLE "we""ird"`) {
		t.Errorf("the name with a quote in it is written as %q", statement)
	}
	if !strings.Contains(statement, `"Full Name" text`) {
		t.Errorf("the name with a space in it is written as %q", statement)
	}
}

// Ident is the one place a name becomes SQL, so it is checked directly as well.
func TestIdentQuotesWhatItIsGiven(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		// The ordinary name, which is quoted anyway.
		"orders": `"orders"`,
		// Case and accents, which fold or sort differently without quotes.
		"Customer": `"Customer"`,
		"endereço": `"endereço"`,
		// A reserved word, which is the case the conditional rule gets wrong
		// the day the server adds one.
		"select": `"select"`,
		// A quote inside, which ends the identifier early unless it is
		// doubled — the injection this closes.
		`we"ird`: `"we""ird"`,
		`"`:      `""""`,
		// A line break, which PostgreSQL allows inside quotes.
		"two\nlines": "\"two\nlines\"",
	} {
		if got := ddl.Ident(catalog.NewName(raw)); got != want {
			t.Errorf("Ident(%q) = %q, want %q", raw, got, want)
		}
	}
}

// Where a column's value comes from: one of three things and never two.
func TestAColumnIsWrittenWithWhereItsValueComesFrom(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name: name("sales"),
		Tables: []catalog.Table{{
			Name: name("shapes"),
			Columns: []catalog.Column{
				{Name: name("plain"), Position: 1, Type: catalog.NewTypeName("integer"), Default: "42"},
				{Name: name("made"), Position: 2, Type: catalog.NewTypeName("integer"),
					Default: "(plain * 2)", Generated: "stored"},
				{Name: name("sorted"), Position: 3, Type: catalog.NewTypeName("text"),
					Collation: name("C")},
			},
		}},
	}

	statement := only(t, ddl.Of(schema))

	for _, want := range []string{
		`"plain" integer DEFAULT 42`,
		`"made" integer GENERATED ALWAYS AS ((plain * 2)) STORED`,
		`"sorted" text COLLATE "C"`,
	} {
		if !strings.Contains(statement, want) {
			t.Errorf("the statement does not write %q:\n%s", want, statement)
		}
	}

	// A generated column keeps its expression where a default would be, and
	// writing both would be a column the server refuses.
	if strings.Contains(statement, `"made" integer DEFAULT`) {
		t.Errorf("a generated column is written with a default as well:\n%s", statement)
	}
}

// A column with no collation is written without one, because a type that
// cannot be collated refuses COLLATE rather than ignoring it.
func TestAColumnWithNoCollationIsWrittenWithout(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name: name("sales"),
		Tables: []catalog.Table{{
			Name:    name("orders"),
			Columns: []catalog.Column{{Name: name("id"), Position: 1, Type: catalog.NewTypeName("integer")}},
		}},
	}

	mustNotWrite(t, ddl.Of(schema), "COLLATE")
}

// A sequence is written with every parameter that decides what it hands out.
//
// None of them defaulted: a copy made with the defaults counts differently from
// the original, which is a data fault produced by a copy of the structure.
func TestASequenceIsWrittenWithEveryParameter(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name: name("sales"),
		Sequences: []catalog.Sequence{{
			Name: name("counter"), Type: catalog.NewTypeName("smallint"),
			Start: 100, Increment: 5, Min: 10, Max: 30000, Cache: 20, Cycle: true,
		}},
	}

	want := `CREATE SEQUENCE "counter"
    AS smallint
    INCREMENT BY 5
    MINVALUE 10
    MAXVALUE 30000
    START WITH 100
    CACHE 20
    CYCLE`

	if got := only(t, ddl.Of(schema)); got != want {
		t.Errorf("the sequence is written as\n%s\nwant\n%s", got, want)
	}
}

// ownedSchema is a table whose column's default calls a sequence that the
// column also owns, which is the shape serial produces and the ordinary one.
func ownedSchema(identity string) catalog.Schema {
	column := catalog.Column{Name: name("id"), Position: 1, Type: catalog.NewTypeName("integer")}

	switch identity {
	case "":
		column.Default = "nextval('counted_id_seq'::regclass)"
	default:
		column.Identity = identity
	}

	return catalog.Schema{
		Name:   name("sales"),
		Tables: []catalog.Table{{Name: name("counted"), Columns: []catalog.Column{column}}},
		Sequences: []catalog.Sequence{{
			Name: name("counted_id_seq"), Type: catalog.NewTypeName("integer"),
			Start: 1, Increment: 1, Min: 1, Max: 2147483647, Cache: 1,
			OwnedBy: catalog.ColumnRef{Table: name("counted"), Column: name("id")},
		}},
		Dependencies: []catalog.Dependency{{
			Object: table("counted"),
			Needs:  catalog.Object{Kind: catalog.ObjectSequence, Name: name("counted_id_seq")},
			Reason: catalog.ReasonDefault,
		}},
	}
}

// The sequence comes first, the table second, and the ownership last.
//
// It is the only order that works, and it is why ownership is a statement of
// its own: the table needs the sequence because its default calls it, and
// OWNED BY needs the table. Written as one statement they would need each
// other.
func TestOwnershipIsWrittenAfterBothTheSequenceAndTheTable(t *testing.T) {
	t.Parallel()

	script := ddl.Of(ownedSchema(""))

	if len(script.Statements) != 3 {
		t.Fatalf("the script holds %v, want three statements", script.Statements)
	}
	if !strings.HasPrefix(script.Statements[0], `CREATE SEQUENCE "counted_id_seq"`) {
		t.Errorf("the first statement is %q, want the sequence", script.Statements[0])
	}
	if !strings.HasPrefix(script.Statements[1], `CREATE TABLE "counted"`) {
		t.Errorf("the second statement is %q, want the table", script.Statements[1])
	}

	want := `ALTER SEQUENCE "counted_id_seq" OWNED BY "counted"."id"`
	if script.Statements[2] != want {
		t.Errorf("the last statement is %q, want %q", script.Statements[2], want)
	}
}

// The sequence behind an identity column is not written at all — the column's
// own clause creates it — and its parameters go into that clause instead.
//
// Writing a CREATE SEQUENCE beside it would either fail on the name or leave a
// second counter nobody uses, and a clause that said only AS IDENTITY would
// lose the sequence's name and its numbers.
func TestTheSequenceBehindAnIdentityColumnIsWrittenAsTheColumnsClause(t *testing.T) {
	t.Parallel()

	script := ddl.Of(ownedSchema("always"))

	mustNotWrite(t, script, "CREATE SEQUENCE")
	mustNotWrite(t, script, "OWNED BY")
	mustWrite(t, script, `"id" integer GENERATED ALWAYS AS IDENTITY `+
		`(SEQUENCE NAME "counted_id_seq" INCREMENT BY 1 `+
		`MINVALUE 1 MAXVALUE 2147483647 START WITH 1 CACHE 1)`)
}

// A column that is an identity by default says so, because the two are
// different: one refuses a value the application supplies and the other takes
// it.
func TestAnIdentityByDefaultIsWrittenAsOne(t *testing.T) {
	t.Parallel()

	mustWrite(t, ddl.Of(ownedSchema("by default")), "GENERATED BY DEFAULT AS IDENTITY")
}

// constrainedSchema is a table with one of every constraint the model compares.
func constrainedSchema() catalog.Schema {
	return catalog.Schema{
		Name: name("sales"),
		Tables: []catalog.Table{{
			Name:    name("orders"),
			Columns: []catalog.Column{{Name: name("id"), Position: 1, Type: catalog.NewTypeName("integer")}},
			Constraints: []catalog.Constraint{
				{Name: name("orders_pk"), Kind: catalog.ConstraintPrimaryKey,
					Definition: "PRIMARY KEY (id)"},
				{Name: name("orders_check"), Kind: catalog.ConstraintCheck,
					Definition: "CHECK ((id > 0))"},
				{Name: name("orders_to_customers"), Kind: catalog.ConstraintForeignKey,
					Definition: "FOREIGN KEY (id) REFERENCES customers(id)"},
			},
		}},
	}
}

// Everything but a foreign key goes inside the table.
func TestAConstraintIsWrittenInsideTheTable(t *testing.T) {
	t.Parallel()

	script := constrainedSchema()

	mustWrite(t, ddl.Of(script), `CONSTRAINT "orders_pk" PRIMARY KEY (id)`)
	mustWrite(t, ddl.Of(script), `CONSTRAINT "orders_check" CHECK ((id > 0))`)
}

// A foreign key is added afterwards, always.
//
// A circle of them cannot be written any other way — no CREATE TABLE order
// produces one — and once one has to be deferred, deferring all of them is what
// keeps the script from depending on which key happened to be in a circle.
func TestAForeignKeyIsAddedAfterTheTables(t *testing.T) {
	t.Parallel()

	script := ddl.Of(constrainedSchema())

	if len(script.Statements) != 2 {
		t.Fatalf("the script holds %v, want the table and the key", script.Statements)
	}
	if strings.Contains(script.Statements[0], "FOREIGN KEY") {
		t.Errorf("the key is written inside the table:\n%s", script.Statements[0])
	}

	want := "ALTER TABLE \"orders\"\n    ADD CONSTRAINT \"orders_to_customers\" " +
		"FOREIGN KEY (id) REFERENCES customers(id)"
	if script.Statements[1] != want {
		t.Errorf("the key is written as %q, want %q", script.Statements[1], want)
	}
}

// A circle of foreign keys is written, which is the point of deferring them.
func TestACircleOfForeignKeysIsWritten(t *testing.T) {
	t.Parallel()

	circle := func(from, to string) catalog.Table {
		return catalog.Table{
			Name:    name(from),
			Columns: []catalog.Column{{Name: name("id"), Position: 1, Type: catalog.NewTypeName("integer")}},
			Constraints: []catalog.Constraint{{
				Name: name(from + "_to_" + to), Kind: catalog.ConstraintForeignKey,
				Definition: "FOREIGN KEY (id) REFERENCES " + to + "(id)",
			}},
		}
	}

	schema := catalog.Schema{
		Name:   name("sales"),
		Tables: []catalog.Table{circle("a", "b"), circle("b", "a")},
		Dependencies: []catalog.Dependency{
			{Object: table("a"), Needs: table("b"), Reason: catalog.ReasonForeignKey},
			{Object: table("b"), Needs: table("a"), Reason: catalog.ReasonForeignKey},
		},
	}

	script := ddl.Of(schema)

	if len(script.Statements) != 4 {
		t.Fatalf("the script holds %v, want both tables and both keys", script.Statements)
	}
	for i, want := range []string{"CREATE TABLE", "CREATE TABLE", "ALTER TABLE", "ALTER TABLE"} {
		if !strings.HasPrefix(script.Statements[i], want) {
			t.Errorf("statement %d is %q, want one starting %q", i, script.Statements[i], want)
		}
	}
}

// A child names its parents in the order they were declared, because that order
// decides where the inherited columns come.
func TestATableIsWrittenWithWhatItInherits(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name: name("sales"),
		Tables: []catalog.Table{
			{Name: name("child"), Inherits: []catalog.Name{name("second"), name("first")}},
			{Name: name("first")},
			{Name: name("second")},
		},
		Dependencies: []catalog.Dependency{
			{Object: table("child"), Needs: table("first"), Reason: catalog.ReasonInheritance},
			{Object: table("child"), Needs: table("second"), Reason: catalog.ReasonInheritance},
		},
	}

	mustWrite(t, ddl.Of(schema), `CREATE TABLE "child" () INHERITS ("second", "first")`)
}

// An index stands on its own and is written as the server rendered it: the
// expression of an index by expression, the WHERE of a partial one and the
// payload of an INCLUDE survive nowhere else.
func TestAnIndexIsWrittenAsTheServerRenderedIt(t *testing.T) {
	t.Parallel()

	definition := "CREATE INDEX partial ON indexed USING btree (email) WHERE (status = 'active'::text)"

	schema := catalog.Schema{
		Name: name("sales"),
		Tables: []catalog.Table{{
			Name:    name("indexed"),
			Indexes: []catalog.Index{{Name: name("partial"), Definition: definition}},
		}},
	}

	script := ddl.Of(schema)
	if len(script.Statements) != 2 || script.Statements[1] != definition {
		t.Errorf("the index is written as %v, want the definition as it came", script.Statements)
	}
}

// A view is written with its query and, after it, the clause the query does not
// carry: a view that lost its check option accepts writes the original refuses.
func TestAViewIsWrittenWithItsCheckOption(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name: name("sales"),
		Views: []catalog.View{{
			Name:        name("checked"),
			Definition:  "SELECT id FROM orders",
			CheckOption: "cascaded",
		}},
	}

	want := "CREATE VIEW \"checked\" AS\n    SELECT id FROM orders\n    WITH CASCADED CHECK OPTION"
	if got := only(t, ddl.Of(schema)); got != want {
		t.Errorf("the view is written as\n%s\nwant\n%s", got, want)
	}
}

// partitionedSchema is a partitioned table, one of its partitions, and a view
// over that partition.
func partitionedSchema() catalog.Schema {
	return catalog.Schema{
		Name: name("sales"),
		Tables: []catalog.Table{
			{Name: name("measurements"), Partitioned: true},
			{Name: name("measurements_2026"), Partition: true,
				Inherits: []catalog.Name{name("measurements")}},
			{Name: name("orders")},
		},
		Views: []catalog.View{
			{Name: name("recent"), Definition: "SELECT * FROM measurements_2026"},
		},
		Dependencies: []catalog.Dependency{
			{Object: table("measurements_2026"), Needs: table("measurements"),
				Reason: catalog.ReasonInheritance},
			{Object: view("recent"), Needs: table("measurements_2026"), Reason: catalog.ReasonQuery},
		},
	}
}

// A table that takes part in partitioning is declined by name rather than
// written as something else.
//
// Written without its PARTITION BY it is an ordinary table, and a partition
// written without its bounds is a copy that holds the wrong rows and says
// nothing about it. Declining is what ADR-0007 asks for: an object that is not
// compared is named as not compared.
func TestAPartitionedTableIsDeclinedRatherThanWrittenAsAnOrdinaryOne(t *testing.T) {
	t.Parallel()

	script := ddl.Of(partitionedSchema())

	mustNotWrite(t, script, `CREATE TABLE "measurements"`)
	mustNotWrite(t, script, `CREATE TABLE "measurements_2026"`)
	mustWrite(t, script, `CREATE TABLE "orders"`)

	if len(script.Omitted) != 3 {
		t.Fatalf("the script omitted %v, want the two tables and the view over one", script.Omitted)
	}
	mustWrite(t, script, `-- not written: the table "measurements", because it is divided into partitions`)
	mustWrite(t, script, `-- not written: the table "measurements_2026", because it is a partition`)
}

// What needs something that was not written is not written either.
//
// A view over a table the script declined is a statement that fails on the
// target, halfway through, on a database that is now part built. Declining it
// here is where the reason can still be given.
func TestWhatNeedsSomethingDeclinedIsDeclinedToo(t *testing.T) {
	t.Parallel()

	script := ddl.Of(partitionedSchema())

	mustNotWrite(t, script, `CREATE VIEW "recent"`)
	mustWrite(t, script,
		`-- not written: the view "recent", because it needs the table "measurements_2026"`)
}

// A schema with nothing declined says nothing about omissions, so a script that
// is complete reads as one.
func TestAScriptThatWroteEverythingSaysNothingAboutOmissions(t *testing.T) {
	t.Parallel()

	script := ddl.Of(constrainedSchema())

	if len(script.Omitted) != 0 {
		t.Errorf("a complete script reports the omissions %v", script.Omitted)
	}
	if strings.Contains(script.String(), "not written") {
		t.Errorf("a complete script mentions omissions:\n%s", script.String())
	}
}

// The script is the same text every time it is written, which is what makes a
// generated file a file that changes only when the schema does.
func TestTheSameSchemaIsWrittenTheSameWayEveryTime(t *testing.T) {
	t.Parallel()

	schema := partitionedSchema()

	if first, second := ddl.Of(schema).String(), ddl.Of(schema).String(); first != second {
		t.Errorf("two writings of one schema differ:\n%s\n%s", first, second)
	}
}

// An empty schema is a schema, and writing one is not a failure: it is what
// somebody sees the moment after they create it.
func TestAnEmptySchemaIsWrittenAsNothing(t *testing.T) {
	t.Parallel()

	script := ddl.Of(catalog.Schema{Name: name("sales")})

	if len(script.Statements) != 0 || script.String() != "" {
		t.Errorf("an empty schema is written as %q", script.String())
	}
}

// An identity column whose sequence the model does not hold is still an
// identity column.
//
// The reader always brings the sequence along, so this is not a schema read
// off a server — it is one built by hand, which is what a comparison target
// written by a person is. Writing the clause without its options leaves the
// server to make a counter with the defaults, which is what somebody who wrote
// down only "identity" asked for; refusing to write the column would lose it
// altogether.
func TestAnIdentityColumnWithNoSequenceInTheModelIsStillWritten(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name: name("sales"),
		Tables: []catalog.Table{{
			Name: name("orders"),
			Columns: []catalog.Column{{
				Name: name("id"), Position: 1,
				Type: catalog.NewTypeName("integer"), Identity: "always", NotNull: true,
			}},
		}},
	}

	want := `"id" integer GENERATED ALWAYS AS IDENTITY NOT NULL`
	if got := only(t, ddl.Of(schema)); !strings.Contains(got, want) {
		t.Errorf("the column is written as %q, want it to contain %q", got, want)
	}
}

// A constraint declared NOT VALID is added afterwards, not written inside the
// table.
//
// CREATE TABLE parses the words and ignores them: the constraint is created
// validated. Two things go wrong at once. The copy renders back without them,
// so it compares unequal to what it was copied from — a change nobody made. And
// the server scans the whole table to validate a constraint somebody
// deliberately declared without validating, taking a lock on exactly the tables
// big enough for NOT VALID to have been worth writing.
func TestAConstraintDeclaredNotValidIsAddedAfterwards(t *testing.T) {
	t.Parallel()

	schema := catalog.Schema{
		Name: name("sales"),
		Tables: []catalog.Table{{
			Name:    name("orders"),
			Columns: []catalog.Column{{Name: name("amount"), Position: 1, Type: catalog.NewTypeName("numeric")}},
			Constraints: []catalog.Constraint{
				{Name: name("orders_positive"), Kind: catalog.ConstraintCheck,
					Definition: "CHECK ((amount > 0)) NOT VALID"},
				{Name: name("orders_sane"), Kind: catalog.ConstraintCheck,
					Definition: "CHECK ((amount < 1000))"},
			},
		}},
	}

	script := ddl.Of(schema)

	if len(script.Statements) != 2 {
		t.Fatalf("the script holds %v, want the table and the deferred check", script.Statements)
	}

	// The validated one goes inside the table; the other cannot.
	if !strings.Contains(script.Statements[0], `CONSTRAINT "orders_sane" CHECK ((amount < 1000))`) {
		t.Errorf("the ordinary check is not inside the table:\n%s", script.Statements[0])
	}
	if strings.Contains(script.Statements[0], "NOT VALID") {
		t.Errorf("the unvalidated check is inside the table, where it would be validated:\n%s",
			script.Statements[0])
	}

	want := "ALTER TABLE \"orders\"\n    ADD CONSTRAINT \"orders_positive\" CHECK ((amount > 0)) NOT VALID"
	if script.Statements[1] != want {
		t.Errorf("the unvalidated check is added as %q, want %q", script.Statements[1], want)
	}
}
