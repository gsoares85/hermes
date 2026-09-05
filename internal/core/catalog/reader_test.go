package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
	"github.com/gsoares85/hermes/internal/driver"
)

// answers stands in for a server: it matches a query by the catalog table it
// reads from and answers the rows a test wants back.
//
// Keyed by that rather than by the order the reader asks, so that rearranging
// the reads is a refactor and not a broken test. What these tests are about is
// the model that comes out, not the sequence of questions that produced it.
type answers struct {
	rows map[string][][]any
	err  map[string]error
}

func (a *answers) Query(_ context.Context, sql string, _ ...any) driver.Rows {
	// Matched on the FROM clause rather than on the query text: every one of
	// these reads joins pg_class, so a plain substring would answer the column
	// query with the rows meant for the table one.
	for table, err := range a.err {
		if reads(sql, table) {
			return &fakeRows{err: err}
		}
	}

	for table, rows := range a.rows {
		if reads(sql, table) {
			return &fakeRows{rows: rows}
		}
	}

	return &fakeRows{}
}

func reads(sql, table string) bool {
	return strings.Contains(sql, "FROM pg_catalog."+table)
}

type fakeRows struct {
	rows    [][]any
	current int
	err     error
}

func (f *fakeRows) Next() bool {
	if f.err != nil || f.current >= len(f.rows) {
		return false
	}
	f.current++

	return true
}

// Scan copies a row of the fixture into the destinations the reader asked for.
//
// A destination the fixture does not match is answered as an error rather than
// left to panic: a fixture written with the wrong number or the wrong kind of
// value is a mistake in the test, and it should read as one instead of as a
// stack trace.
func (f *fakeRows) Scan(dest ...any) error {
	row := f.rows[f.current-1]
	if len(dest) != len(row) {
		return fmt.Errorf("the fixture row has %d values and the reader asked for %d",
			len(row), len(dest))
	}

	for i := range dest {
		if err := assign(dest[i], row[i]); err != nil {
			return fmt.Errorf("value %d: %w", i, err)
		}
	}

	return nil
}

func assign(into, value any) error {
	switch target := into.(type) {
	case *string:
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%v is not text", value)
		}
		*target = text
	case *int:
		number, ok := value.(int)
		if !ok {
			return fmt.Errorf("%v is not a number", value)
		}
		*target = number
	case *bool:
		flag, ok := value.(bool)
		if !ok {
			return fmt.Errorf("%v is not a boolean", value)
		}
		*target = flag
	case **string:
		// The shape a column the catalog can answer NULL for arrives in. The
		// fixture writes plain text for a value and nil for NULL.
		if value == nil {
			*target = nil

			return nil
		}

		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%v is neither text nor NULL", value)
		}
		*target = &text
	default:
		return fmt.Errorf("the fake does not know the destination %T", into)
	}

	return nil
}

func (f *fakeRows) Err() error { return f.err }
func (f *fakeRows) Close()     {}

// existing is the namespace answer for a schema that is there.
func existing() [][]any { return [][]any{{"sales"}} }

// A schema that is not there is not an empty schema, and the difference decides
// what the layer above says. "This database has no such schema" is a different
// sentence from "this schema has nothing in it", and a diff whose target is
// missing must not compare against emptiness and offer to create the world.
func TestReadingASchemaThatIsNotThere(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{"pg_namespace": {}}}

	_, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("missing"))
	if !errors.Is(err, catalog.ErrSchemaNotFound) {
		t.Errorf("Read() = %v, want ErrSchemaNotFound", err)
	}
}

// An empty schema is a schema, and reading one is not a failure: it is what
// somebody sees the moment after they create it.
func TestReadingAnEmptySchema(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{"pg_namespace": existing()}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if schema.Name.String() != "sales" {
		t.Errorf("the schema is named %q, want sales", schema.Name)
	}
	if len(schema.Tables) != 0 {
		t.Errorf("an empty schema came back with %d tables", len(schema.Tables))
	}
}

// Columns arrive in a query of their own and have to land on the table they
// belong to. Getting this wrong is not a crash — it is a column silently
// attached to the wrong table, which a diff reports as one table losing it and
// another gaining it.
func TestColumnsLandOnTheirOwnTable(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace": existing(),
		"pg_class":     {{"orders"}, {"customers"}},
		"pg_attribute": {
			{"customers", "name", 1, "text", false, nil, "", "", nil},
			{"orders", "id", 1, "int4", true, nil, "", "", nil},
			{"orders", "amount", 2, "numeric(10,2)", false, nil, "", "", nil},
		},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	got := map[string][]string{}
	for _, table := range schema.Tables {
		for _, column := range table.Columns {
			got[table.Name.String()] = append(got[table.Name.String()], column.Name.String())
		}
	}

	if len(got["orders"]) != 2 || got["orders"][0] != "id" || got["orders"][1] != "amount" {
		t.Errorf("orders has columns %v, want [id amount]", got["orders"])
	}
	if len(got["customers"]) != 1 || got["customers"][0] != "name" {
		t.Errorf("customers has columns %v, want [name]", got["customers"])
	}
}

// A column of a table this reader did not list belongs to nothing, and dropping
// it silently would hide a mistake in the queries — the two are supposed to see
// the same set of tables.
func TestAColumnOfAnUnknownTableIsAFault(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace": existing(),
		"pg_class":     {{"orders"}},
		"pg_attribute": {{"somewhere_else", "id", 1, "int4", true, nil, "", "", nil}},
	}}

	if _, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales")); err == nil {
		t.Error("Read() = nil for a column belonging to a table that was not listed")
	}
}

// The type comes back folded, which is the point of reading it through this
// package rather than keeping the text the server sent.
func TestAColumnCarriesTheFoldedType(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace": existing(),
		"pg_class":     {{"orders"}},
		"pg_attribute": {{"orders", "created", 1, "timestamptz(3)", true, nil, "", "", nil}},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	if got := schema.Tables[0].Columns[0].Type.String(); got != "timestamp(3) with time zone" {
		t.Errorf("the column type is %q, want it folded", got)
	}
}

// Everything a column can carry, read off three rows, so that a field lost
// between the query and the model is caught here rather than in a diff.
func TestAColumnCarriesWhatTheCatalogSaidAboutIt(t *testing.T) {
	t.Parallel()

	def, collation := "nextval(:seq:)", "en_US.utf8"
	server := &answers{rows: map[string][][]any{
		"pg_namespace": existing(),
		"pg_class":     {{"orders"}},
		"pg_attribute": {
			{"orders", "id", 1, "int4", true, def, "a", "", nil},
			{"orders", "label", 2, "text", false, nil, "", "", collation},
			{"orders", "total", 3, "numeric", false, nil, "", "s", nil},
		},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}
	columns := schema.Tables[0].Columns

	if !columns[0].NotNull || columns[0].Default != def || columns[0].Identity != "always" {
		t.Errorf("the identity column came back as %+v", columns[0])
	}
	if columns[1].NotNull || columns[1].Collation.String() != collation {
		t.Errorf("the collated column came back as %+v", columns[1])
	}
	if columns[2].Generated != "stored" {
		t.Errorf("the generated column came back as %+v", columns[2])
	}
}

// A failure reading any of the queries is the read failing. Answering a partial
// schema would be worse than answering nothing: a diff would compare against it
// and offer to drop everything the failed query would have listed.
func TestAFailedQueryFailsTheRead(t *testing.T) {
	t.Parallel()

	for _, table := range []string{"pg_namespace", "pg_class", "pg_attribute"} {
		t.Run(table, func(t *testing.T) {
			t.Parallel()

			server := &answers{
				rows: map[string][][]any{
					"pg_namespace": existing(),
					"pg_class":     {{"orders"}},
					"pg_attribute": {},
				},
				err: map[string]error{table: errors.New("the server went away")},
			}

			_, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
			if err == nil {
				t.Fatalf("Read() = nil when %s could not be read", table)
			}
			if !strings.Contains(err.Error(), "the server went away") {
				t.Errorf("Read() = %v, want the reason to survive", err)
			}
		})
	}
}

// The schema name reaches the server as a parameter, never spliced into the
// text of a query. It arrives from a person, and a name is exactly the sort of
// thing that carries a quote.
func TestTheSchemaNameIsNeverSplicedIntoTheQuery(t *testing.T) {
	t.Parallel()

	recorder := &recordingQuerier{
		inner: &answers{rows: map[string][][]any{"pg_namespace": existing()}},
	}

	name := `awkward" OR 1=1 --`
	if _, err := catalog.NewReader(recorder).Read(t.Context(), catalog.NewName(name)); err != nil {
		t.Fatalf("Read() = %v", err)
	}

	for _, sql := range recorder.sql {
		if strings.Contains(sql, "awkward") {
			t.Errorf("the name is in the text of a query: %s", sql)
		}
	}
	if !recorder.carried(name) {
		t.Errorf("the name never arrived as an argument: %v", recorder.args)
	}
}

type recordingQuerier struct {
	inner catalog.Querier
	sql   []string
	args  []any
}

func (r *recordingQuerier) Query(ctx context.Context, sql string, args ...any) driver.Rows {
	r.sql = append(r.sql, sql)
	r.args = append(r.args, args...)

	return r.inner.Query(ctx, sql, args...)
}

func (r *recordingQuerier) carried(value string) bool {
	for _, argument := range r.args {
		if argument == value {
			return true
		}
	}

	return false
}

// A name that cannot address a schema is refused before anything is sent.
func TestReadingRefusesANameThatCannotAddressASchema(t *testing.T) {
	t.Parallel()

	for name, raw := range map[string]string{
		"empty":  "",
		"quoted": `"sales"`,
		"a NUL":  "sa\x00les",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			recorder := &recordingQuerier{
				inner: &answers{rows: map[string][][]any{"pg_namespace": existing()}},
			}

			if _, err := catalog.NewReader(recorder).Read(t.Context(), catalog.NewName(raw)); err == nil {
				t.Errorf("Read(%q) = nil, want a refusal", raw)
			}
			if len(recorder.sql) != 0 {
				t.Errorf("Read(%q) sent %d queries before refusing", raw, len(recorder.sql))
			}
		})
	}
}

// The model comes out sorted, whatever order the server answered in. It is the
// property the package rests on, asserted here at the boundary a real read
// crosses rather than only on a model built by hand.
func TestAReadSchemaComesOutSorted(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace": existing(),
		"pg_class":     {{"orders"}, {"customers"}},
		"pg_attribute": {
			{"orders", "amount", 2, "numeric", false, nil, "", "", nil},
			{"orders", "id", 1, "int4", true, nil, "", "", nil},
		},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	if schema.Tables[0].Name.String() != "customers" || schema.Tables[1].Name.String() != "orders" {
		t.Errorf("the tables came back in the order the server answered: %v", schema.Tables)
	}
	if schema.Tables[1].Columns[0].Name.String() != "id" {
		t.Errorf("the columns came back out of table order: %v", schema.Tables[1].Columns)
	}
}

// A type declared beside the column that uses it comes back qualified from
// format_type, and the qualifier has to go: the model is of one schema, so a
// type inside it is part of that schema. Keeping it would make the same schema
// compare unequal to itself the moment it is read under another name — which is
// what a diff between an environment and its copy does.
func TestATypeOfThisSchemaLosesTheSchemaFromItsName(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace": existing(),
		"pg_class":     {{"orders"}},
		"pg_attribute": {
			{"orders", "own", 1, "sales.mood", false, nil, "", "", nil},
			{"orders", "own_array", 2, "sales.mood[]", false, nil, "", "", nil},
			{"orders", "elsewhere", 3, "public.mood", false, nil, "", "", nil},
			{"orders", "builtin", 4, "int4", false, nil, "", "", nil},
		},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}
	columns := schema.Tables[0].Columns

	if got := columns[0].Type.String(); got != "mood" {
		t.Errorf("a type of this schema reads as %q, want mood", got)
	}
	if got := columns[1].Type.String(); got != "mood[]" {
		t.Errorf("an array of a type of this schema reads as %q, want mood[]", got)
	}

	// A qualifier naming somewhere else is a real reference to somewhere else,
	// and a schema pointing at public.mood is genuinely different from one
	// pointing at its own.
	if got := columns[2].Type.String(); got != "public.mood" {
		t.Errorf("a type of another schema reads as %q, want it kept whole", got)
	}
	if got := columns[3].Type.String(); got != "integer" {
		t.Errorf("a built-in reads as %q", got)
	}
}

// The same, for a schema whose name format_type has to quote.
func TestAQuotedSchemaIsStrippedFromATypeToo(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace": {{"My Sales"}},
		"pg_class":     {{"orders"}},
		"pg_attribute": {{"orders", "own", 1, `"My Sales".mood`, false, nil, "", "", nil}},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("My Sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	if got := schema.Tables[0].Columns[0].Type.String(); got != "mood" {
		t.Errorf("the type reads as %q, want mood", got)
	}
}
