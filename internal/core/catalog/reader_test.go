package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
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
//
// A key is the catalog table, and where one table is read by more than one
// query it carries a fragment of that query as well — "pg_class ARRAY['r', 'p']" is
// the read of the tables and "pg_class relkind OPERATOR(pg_catalog.=) 'v'" the read of the views. See
// reads.
type answers struct {
	rows map[string][][]any
	err  map[string]error

	// The search path queries, which every read begins and ends with. They are
	// answered here rather than through the fixtures because they read no
	// catalog table and no test is about them; the ones that are set these.
	askingFails, scopingFails, restoringFails error

	// cancelDuring names the catalog table whose read gives up, standing in for
	// a person pressing cancel while the schema is being read. The read that
	// matches it cancels the context and answers the cancellation.
	cancelDuring string
	cancel       context.CancelFunc

	// snapshots counts the transactions opened and left, so a test can say the
	// read happened inside one. snapshotFails is what a server that refuses to
	// open one answers, and inTransaction is the caller that already had one.
	snapshots     atomic.Int64
	left          atomic.Int64
	snapshotFails error
	inTransaction bool

	// What the caller's transaction answers when asked what it is. The
	// defaults are what BeginSnapshot would have produced, so a test about
	// something else does not have to say. kindFails is a transaction that
	// cannot answer at all, which is what a closed one does.
	isolation, readOnly string
	kindFails           error

	// restored counts the calls that put the path back, so a test can say the
	// reader tried to. restoredLive records whether the context the path was
	// put back through was still usable. It is the whole point of the test that sets
	// cancelDuring: restoring through a cancelled context fails exactly when
	// restoring matters.
	restored     atomic.Int64
	restoredLive bool
}

// The path the session is on before a read scopes it, and what it goes back to.
const pathBefore = "public"

// kindOfTransaction answers the two settings the reader asks about, defaulting
// to the kind BeginSnapshot opens.
func (a *answers) kindOfTransaction() []any {
	isolation, readOnly := a.isolation, a.readOnly
	if isolation == "" {
		isolation = "repeatable read"
	}

	if readOnly == "" {
		readOnly = "on"
	}

	return []any{isolation, readOnly}
}

func (a *answers) BeginSnapshot(context.Context) error {
	if a.inTransaction {
		return driver.ErrTransactionActive
	}

	if a.snapshotFails != nil {
		return a.snapshotFails
	}

	a.snapshots.Add(1)

	return nil
}

func (a *answers) Rollback(context.Context) error {
	a.left.Add(1)

	return nil
}

func (a *answers) Query(ctx context.Context, sql string, _ ...any) driver.Rows {
	if setting, isSetting := a.searchPath(ctx, sql); isSetting {
		return setting
	}

	if a.cancelDuring != "" && reads(sql, a.cancelDuring) {
		a.cancel()

		return &fakeRows{err: ctx.Err()}
	}

	if key := match(sql, a.err); key != "" {
		return &fakeRows{err: a.err[key]}
	}

	if key := match(sql, a.rows); key != "" {
		return &fakeRows{rows: a.rows[key]}
	}

	return &fakeRows{}
}

// searchPath answers the queries that read and set the path, the way a server
// answers them: with the value the setting was left at.
//
// They are told apart by what they call rather than by the whole text, so that
// rewording one is a refactor and not a broken double. The scoping call is the
// one that quotes its argument, which is what separates it from the call that
// puts the old value back.
func (a *answers) searchPath(ctx context.Context, sql string) (driver.Rows, bool) {
	switch {
	case strings.Contains(sql, "transaction_isolation"):
		if a.kindFails != nil {
			return &fakeRows{err: a.kindFails}, true
		}

		return &fakeRows{rows: [][]any{a.kindOfTransaction()}}, true
	case strings.Contains(sql, "current_setting"):
		return &fakeRows{rows: [][]any{{pathBefore}}, err: a.askingFails}, true
	case strings.Contains(sql, "quote_ident"):
		return &fakeRows{rows: [][]any{{"scoped"}}, err: a.scopingFails}, true
	case strings.Contains(sql, "set_config"):
		// The only set_config left is the one putting the path back, and
		// whether its context is still usable is what a cancelled read is
		// judged on.
		a.restored.Add(1)
		a.restoredLive = ctx.Err() == nil

		return &fakeRows{rows: [][]any{{pathBefore}}, err: a.restoringFails}, true
	default:
		return nil, false
	}
}

// match answers the most specific fixture key the query is described by.
//
// Most specific, because two keys can describe one query: tables and views are
// both rows of pg_class and only the relkind tells them apart, so "pg_class"
// alone would answer the view query with the rows meant for the table one. The
// longest key that matches wins, which leaves the plain name meaning what it
// always meant.
func match[T any](sql string, fixtures map[string]T) string {
	best := ""

	for key := range fixtures {
		if reads(sql, key) && len(key) > len(best) {
			best = key
		}
	}

	return best
}

// reads reports whether a query is the one a fixture key describes.
//
// The key is the catalog table the query reads from, optionally followed by a
// fragment of the query that tells it apart from another read of the same
// table.
//
// The table is compared against the first FROM and not against any mention.
// Every one of these joins pg_class, and the index query mentions pg_constraint
// in the subquery that excludes constraint-backed indexes — so anything looser
// answers one query with the rows meant for another, which arrives as a scan of
// the wrong width rather than as anything that reads like the mistake it is.
func reads(sql, key string) bool {
	const marker = "FROM pg_catalog."

	table, fragment, _ := strings.Cut(key, " ")

	start := strings.Index(sql, marker)
	if start < 0 {
		return false
	}

	rest := sql[start+len(marker):]
	if !strings.HasPrefix(rest, table) || !ends(rest[len(table):]) {
		return false
	}

	return fragment == "" || strings.Contains(sql, fragment)
}

// ends reports whether the identifier stopped here rather than continuing —
// pg_class must not match the query that reads pg_classifier.
func ends(rest string) bool {
	return rest == "" || strings.ContainsRune(" \n\t\r", rune(rest[0]))
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
	case *int64:
		// The shape the parameters of a sequence arrive in: the catalog holds
		// every one of them as bigint.
		number, ok := value.(int64)
		if !ok {
			return fmt.Errorf("%v is not a 64-bit number", value)
		}
		*target = number
	case *bool:
		flag, ok := value.(bool)
		if !ok {
			return fmt.Errorf("%v is not a boolean", value)
		}
		*target = flag
	case *[]string:
		// The shape a column list arrives in — the key of a constraint, the
		// columns of an index.
		if value == nil {
			*target = nil

			return nil
		}

		list, ok := value.([]string)
		if !ok {
			return fmt.Errorf("%v is not a list of names", value)
		}
		*target = list
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
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}, {"customers", "r", false, "p", false, false, nil, nil}},
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
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
		"pg_attribute":             {{"somewhere_else", "id", 1, "int4", true, nil, "", "", nil}},
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
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
		"pg_attribute":             {{"orders", "created", 1, "timestamptz(3)", true, nil, "", "", nil}},
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
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
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
					"pg_namespace":             existing(),
					"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
					"pg_attribute":             {},
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

func (r *recordingQuerier) BeginSnapshot(ctx context.Context) error {
	return r.inner.BeginSnapshot(ctx)
}

func (r *recordingQuerier) Rollback(ctx context.Context) error { return r.inner.Rollback(ctx) }

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
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}, {"customers", "r", false, "p", false, false, nil, nil}},
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
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
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
		"pg_namespace":             {{"My Sales"}},
		"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
		"pg_attribute":             {{"orders", "own", 1, `"My Sales".mood`, false, nil, "", "", nil}},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("My Sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	if got := schema.Tables[0].Columns[0].Type.String(); got != "mood" {
		t.Errorf("the type reads as %q, want mood", got)
	}
}

// The five kinds of constraint the model compares, each landing on the table it
// belongs to and keeping the order of its key.
func TestConstraintsAreReadWithTheirKindAndTheirKeyOrder(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
		"pg_attribute":             {},
		"pg_constraint": {
			{"orders", "orders_pk", "p", "PRIMARY KEY (a, b)", []string{"a", "b"}, nil},
			{"orders", "orders_fk", "f", "FOREIGN KEY (c) REFERENCES other(id)", []string{"c"}, "other"},
			{"orders", "orders_uq", "u", "UNIQUE (b, a)", []string{"b", "a"}, nil},
			{"orders", "orders_ck", "c", "CHECK ((amount > 0))", []string{"amount"}, nil},
			{"orders", "orders_ex", "x", "EXCLUDE USING gist (room WITH =)", []string{"room"}, nil},
		},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	kinds := map[string]catalog.ConstraintKind{}
	keys := map[string][]string{}
	for _, constraint := range schema.Tables[0].Constraints {
		kinds[constraint.Name.String()] = constraint.Kind
		for _, column := range constraint.Columns {
			keys[constraint.Name.String()] = append(keys[constraint.Name.String()], column.String())
		}
	}

	want := map[string]catalog.ConstraintKind{
		"orders_pk": catalog.ConstraintPrimaryKey,
		"orders_fk": catalog.ConstraintForeignKey,
		"orders_uq": catalog.ConstraintUnique,
		"orders_ck": catalog.ConstraintCheck,
		"orders_ex": catalog.ConstraintExclusion,
	}
	for name, kind := range want {
		if kinds[name] != kind {
			t.Errorf("%s reads as %q, want %q", name, kinds[name], kind)
		}
	}

	// The order of a key is part of it: (a, b) and (b, a) are different
	// constraints, and a reader that loses the order gets it right about half
	// the time.
	if got := keys["orders_pk"]; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("the primary key reads as %v, want [a b]", got)
	}
	if got := keys["orders_uq"]; len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Errorf("the unique key reads as %v, want [b a]", got)
	}
}

// A kind from a version newer than this build is carried through rather than
// dropped or flattened into "unknown". ADR-0007 requires an object that is not
// compared to be named as not compared, and this is that case arriving from the
// future.
func TestAConstraintKindThisBuildDoesNotKnowIsCarriedThrough(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
		"pg_attribute":             {},
		"pg_constraint":            {{"orders", "odd", "z", "SOMETHING NEW", []string(nil), nil}},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	if got := schema.Tables[0].Constraints[0].Kind; got != catalog.ConstraintKind("z") {
		t.Errorf("an unknown kind reads as %q, want it carried through", got)
	}
}

// Indexes carry what makes them different from one another, and the key stops
// where the key stops: a column carried by INCLUDE is not one the index is
// ordered by.
func TestIndexesAreReadWithWhatDistinguishesThem(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
		"pg_attribute":             {},
		"pg_index": {
			{"orders", "by_email", true, false,
				"CREATE UNIQUE INDEX by_email ON orders USING btree (email)", []string{"email"}},
			{"orders", "by_expression", false, false,
				"CREATE INDEX by_expression ON orders USING btree (lower(email))", []string(nil)},
		},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}
	indexes := schema.Tables[0].Indexes

	if !indexes[0].Unique || indexes[0].Primary {
		t.Errorf("the unique index reads as %+v", indexes[0])
	}
	// An index by expression names no column, and the definition is where the
	// expression survives.
	if len(indexes[1].Columns) != 0 {
		t.Errorf("an index by expression names columns: %v", indexes[1].Columns)
	}
	if indexes[1].Definition == "" {
		t.Error("an index by expression came back with no definition")
	}
}

// A constraint or an index of a table that was not listed is a fault, for the
// same reason a column of one is.
func TestAConstraintOrIndexOfAnUnknownTableIsAFault(t *testing.T) {
	t.Parallel()

	for name, rows := range map[string]map[string][][]any{
		"a constraint": {
			"pg_namespace":             existing(),
			"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
			"pg_attribute":             {},
			"pg_constraint":            {{"elsewhere", "c", "p", "PRIMARY KEY (a)", []string{"a"}, nil}},
		},
		"an index": {
			"pg_namespace":             existing(),
			"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
			"pg_attribute":             {},
			"pg_index":                 {{"elsewhere", "i", false, false, "CREATE INDEX", []string{"a"}}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			server := &answers{rows: rows}
			if _, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales")); err == nil {
				t.Errorf("Read() = nil for %s of a table that was not listed", name)
			}
		})
	}
}

// A failure reading constraints or indexes fails the whole read, like every
// other query.
func TestAFailureReadingConstraintsOrIndexesFailsTheRead(t *testing.T) {
	t.Parallel()

	for _, table := range []string{"pg_constraint", "pg_index"} {
		t.Run(table, func(t *testing.T) {
			t.Parallel()

			server := &answers{
				rows: map[string][][]any{
					"pg_namespace":             existing(),
					"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
					"pg_attribute":             {},
				},
				err: map[string]error{table: errors.New("the server went away")},
			}

			if _, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales")); err == nil {
				t.Errorf("Read() = nil when %s could not be read", table)
			}
		})
	}
}

// A sequence carries everything that decides what it hands out. A copy made
// with the default start and step counts differently from the original, which
// is a data fault produced by a copy of the structure.
func TestASequenceCarriesTheNumbersItHandsOut(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {},
		"pg_sequence": {{"invoice_number", "bigint",
			int64(100), int64(5), int64(10), int64(9000), int64(20), true, nil, nil}},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if len(schema.Sequences) != 1 {
		t.Fatalf("the schema reads with %d sequences, want 1", len(schema.Sequences))
	}

	sequence := schema.Sequences[0]
	if sequence.Name.String() != "invoice_number" || sequence.Type.String() != "bigint" {
		t.Errorf("the sequence reads as %s %s", sequence.Name, sequence.Type)
	}
	if sequence.Start != 100 || sequence.Increment != 5 {
		t.Errorf("it starts at %d and steps by %d, want 100 and 5", sequence.Start, sequence.Increment)
	}
	if sequence.Min != 10 || sequence.Max != 9000 {
		t.Errorf("its bounds read as %d..%d, want 10..9000", sequence.Min, sequence.Max)
	}
	if sequence.Cache != 20 || !sequence.Cycle {
		t.Errorf("it caches %d and cycles %v, want 20 and true", sequence.Cache, sequence.Cycle)
	}
}

// The column that owns a sequence has to survive the reading. Losing it leaves
// the copy at the other end with a sequence nothing owns and a column whose
// default points at it anyway, which is the orphan a sync is supposed to
// prevent.
func TestASequenceKeepsTheColumnThatOwnsIt(t *testing.T) {
	t.Parallel()

	table, column := "orders", "id"
	server := &answers{rows: map[string][][]any{
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {},
		"pg_sequence": {
			{"orders_id_seq", "integer",
				int64(1), int64(1), int64(1), int64(2147483647), int64(1), false, table, column},
			{"standalone", "bigint",
				int64(1), int64(1), int64(1), int64(9223372036854775807), int64(1), false, nil, nil},
		},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	owned := schema.Sequences[0]
	if !owned.OwnedBy.Valid() {
		t.Errorf("the owned sequence reads as owned by nothing: %+v", owned.OwnedBy)
	}
	if owned.OwnedBy.Table.String() != table || owned.OwnedBy.Column.String() != column {
		t.Errorf("it reads as owned by %+v, want %s.%s", owned.OwnedBy, table, column)
	}

	// A sequence nobody owns is not a fault and not an empty name to be
	// checked for: it is the ordinary shape of a counter somebody made.
	if schema.Sequences[1].OwnedBy.Valid() {
		t.Errorf("a standalone sequence reads as owned by %+v", schema.Sequences[1].OwnedBy)
	}
}

// Views come back with the query behind them, and without the punctuation the
// server wraps it in: what the model holds is what goes after AS, so that a
// definition read from a server and the same one written by hand compare equal.
func TestViewsAreReadWithTheQueryBehindThem(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {},
		"pg_class relkind OPERATOR(pg_catalog.=) 'v'": {
			{"active", " SELECT id FROM orders WHERE status = 'active';", nil, nil},
			{"checked", " SELECT id FROM orders;", "cascaded", nil},
		},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if len(schema.Views) != 2 {
		t.Fatalf("the schema reads with %d views, want 2", len(schema.Views))
	}

	if got := schema.Views[0].Definition; got != "SELECT id FROM orders WHERE status = 'active'" {
		t.Errorf("the definition reads as %q, want it without the space and the semicolon", got)
	}

	// WITH CHECK OPTION is not in the query the server renders, and a reader
	// that only asks for the definition drops the clause that decides whether a
	// write through the view is refused.
	if got := schema.Views[1].CheckOption; got != "cascaded" {
		t.Errorf("the check option reads as %q, want cascaded", got)
	}
	if got := schema.Views[0].CheckOption; got != "" {
		t.Errorf("a view with no check option reads as %q", got)
	}
}

// Sequences and views come out in the one order the model has, whatever order
// the server answered in — the same property the tables have, and for the same
// reason.
func TestSequencesAndViewsComeOutSorted(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {},
		"pg_sequence": {
			{"second", "bigint", int64(1), int64(1), int64(1), int64(2), int64(1), false, nil, nil},
			{"first", "bigint", int64(1), int64(1), int64(1), int64(2), int64(1), false, nil, nil},
		},
		"pg_class relkind OPERATOR(pg_catalog.=) 'v'": {
			{"beta", "SELECT 1", nil, nil},
			{"alpha", "SELECT 1", nil, nil},
		},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	if schema.Sequences[0].Name.String() != "first" {
		t.Errorf("the sequences came back in the order the server answered: %v", schema.Sequences)
	}
	if schema.Views[0].Name.String() != "alpha" {
		t.Errorf("the views came back in the order the server answered: %v", schema.Views)
	}
}

// A failure reading sequences or views fails the whole read, like every other
// query: a schema missing the objects a failed query would have listed is a
// schema a diff would offer to drop them from.
func TestAFailureReadingSequencesOrViewsFailsTheRead(t *testing.T) {
	t.Parallel()

	for _, query := range []string{"pg_sequence", "pg_class relkind OPERATOR(pg_catalog.=) 'v'"} {
		t.Run(query, func(t *testing.T) {
			t.Parallel()

			server := &answers{
				rows: map[string][][]any{
					"pg_namespace":             existing(),
					"pg_class ARRAY['r', 'p']": {},
				},
				err: map[string]error{query: errors.New("the server went away")},
			}

			if _, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales")); err == nil {
				t.Errorf("Read() = nil when %s could not be read", query)
			}
		})
	}
}

// The read points the search path at the schema and puts it back.
//
// The pointing is what makes every expression the server renders independent of
// what the schema is called, and the putting back is what keeps a connection
// handed to the reader from going back to the pool resolving names against
// somewhere else.
func TestTheReadScopesTheSearchPathAndPutsItBack(t *testing.T) {
	t.Parallel()

	recorder := &recordingQuerier{
		inner: &answers{rows: map[string][][]any{"pg_namespace": existing()}},
	}

	if _, err := catalog.NewReader(recorder).Read(t.Context(), catalog.NewName("sales")); err != nil {
		t.Fatalf("Read() = %v", err)
	}

	scoped, restored := -1, -1
	for i, sql := range recorder.sql {
		switch {
		case strings.Contains(sql, "quote_ident"):
			scoped = i
		case strings.Contains(sql, "set_config"):
			restored = i
		}
	}

	if scoped < 0 || restored < 0 {
		t.Fatalf("the read did not scope and restore the path: %v", recorder.sql)
	}
	if scoped > restored {
		t.Errorf("the path was restored at %d and scoped at %d, want the scoping first",
			restored, scoped)
	}
	if restored != len(recorder.sql)-1 {
		t.Errorf("the path was restored at %d of %d queries, want it last",
			restored, len(recorder.sql))
	}

	// It goes back to what it was, read off the session, rather than to a
	// default this package decided on.
	if !recorder.carried("sales") || !recorder.carried(pathBefore) {
		t.Errorf("the schema and the previous path did not both arrive as arguments: %v",
			recorder.args)
	}

	// Everything the read asks of the catalog happens between the two.
	for i, sql := range recorder.sql {
		if strings.Contains(sql, "FROM pg_catalog.pg_class") && (i < scoped || i > restored) {
			t.Errorf("a catalog query at %d falls outside the scoped path", i)
		}
	}
}

// A read that cannot scope the path is a read that would answer expressions
// naming the schema, which is the false positive this is all here to prevent.
// Better to fail than to answer a model that looks right.
func TestAFailureScopingTheSearchPathFailsTheRead(t *testing.T) {
	t.Parallel()

	for name, server := range map[string]*answers{
		"asking":  {rows: map[string][][]any{"pg_namespace": existing()}, askingFails: errors.New("gone")},
		"setting": {rows: map[string][][]any{"pg_namespace": existing()}, scopingFails: errors.New("gone")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
			if err == nil {
				t.Fatal("Read() = nil when the search path could not be scoped")
			}
			if !strings.Contains(err.Error(), "gone") {
				t.Errorf("Read() = %v, want the reason to survive", err)
			}
		})
	}
}

// A path that could not be put back is reported rather than swallowed, even
// though the model itself is complete: the connection goes back to the pool
// pointing at the schema this read happened to want, and the next caller
// resolves their names against it.
func TestAFailureRestoringTheSearchPathIsReported(t *testing.T) {
	t.Parallel()

	server := &answers{
		rows:           map[string][][]any{"pg_namespace": existing()},
		restoringFails: errors.New("gone"),
	}

	_, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err == nil {
		t.Fatal("Read() = nil when the search path could not be put back")
	}
	if !strings.Contains(err.Error(), "gone") {
		t.Errorf("Read() = %v, want the reason to survive", err)
	}
}

// A schema that is not there is answered before the path is touched. Scoping
// for a read that cannot happen would change the session for nothing.
func TestNothingIsScopedForASchemaThatIsNotThere(t *testing.T) {
	t.Parallel()

	recorder := &recordingQuerier{
		inner: &answers{rows: map[string][][]any{"pg_namespace": {}}},
	}

	if _, err := catalog.NewReader(recorder).Read(t.Context(), catalog.NewName("missing")); err == nil {
		t.Fatal("Read() = nil for a schema that is not there")
	}

	for _, sql := range recorder.sql {
		if strings.Contains(sql, "set_config") {
			t.Errorf("the path was changed for a schema that is not there: %s", sql)
		}
	}
}

// A table says what it inherits, in the order it was declared in.
//
// The order is what decides where the inherited columns come, so it is the one
// list in the model that is not sorted — a CREATE TABLE that names the parents
// the other way round produces a table with its columns in another order.
func TestATableIsReadWithWhatItInherits(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {{"child", "r", false, "p", false, false, nil, []string{"second", "first"}}},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	want := []catalog.Name{catalog.NewName("second"), catalog.NewName("first")}
	if got := schema.Tables[0].Inherits; !reflect.DeepEqual(got, want) {
		t.Errorf("the table inherits %v, want %v in the order they were declared", got, want)
	}
}

// A table that takes part in partitioning says so, in both directions.
//
// It is not partitioning modelled: this version does not compare it. It is the
// fact recorded so the DDL writer can decline the table by name — a partitioned
// table written without its PARTITION BY is an ordinary table, and a partition
// written without its bounds is a copy that holds the wrong rows and says
// nothing about it.
func TestATableSaysWhetherItTakesPartInPartitioning(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace": existing(),
		"pg_class ARRAY['r', 'p']": {
			{"measurements", "p", false, "p", false, false, nil, nil},
			{"measurements_2026", "r", true, "p", false, false, nil, []string{"measurements"}},
			{"ordinary", "r", false, "p", false, false, nil, nil},
		},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	for _, want := range []struct {
		name        string
		partitioned bool
		partition   bool
	}{
		{"measurements", true, false},
		{"measurements_2026", false, true},
		{"ordinary", false, false},
	} {
		table := tableNamed(t, schema, want.name)
		if table.Partitioned != want.partitioned || table.Partition != want.partition {
			t.Errorf("%s reads as partitioned=%v partition=%v, want %v and %v",
				want.name, table.Partitioned, table.Partition, want.partitioned, want.partition)
		}
	}
}

func tableNamed(t *testing.T, schema catalog.Schema, name string) catalog.Table {
	t.Helper()

	for _, table := range schema.Tables {
		if table.Name.String() == name {
			return table
		}
	}

	t.Fatalf("the schema has no table %q", name)

	return catalog.Table{}
}

// A column that is nothing in particular says nothing, and the catalog spells
// "nothing" as a NUL rather than as the empty string.
//
// attidentity and attgenerated are both "char" — one byte, zero when there is
// nothing to say — and they arrive here as "\x00". A reader that checked only
// for the empty string would put that byte in the model of every ordinary
// column, where it compares equal to itself and no reading notices; it reaches
// daylight when the DDL writer puts GENERATED ALWAYS AS () and a zero byte into
// a statement, on a schema that has nothing generated in it at all.
func TestAColumnThatIsNeitherGeneratedNorAnIdentitySaysSo(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
		"pg_attribute":             {{"orders", "label", 1, "text", false, nil, "\x00", "\x00", nil}},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	if column := schema.Tables[0].Columns[0]; column.Identity != "" || column.Generated != "" {
		t.Errorf("an ordinary column came back as %+v", column)
	}
}

// A read that is cancelled still puts the search path back.
//
// This is the case restoring exists for, and the one it used to fail. The path
// is only pointed somewhere else while a read is running, so the read ending
// early is the moment it matters most — and a person pressing cancel is how a
// read most often ends early, because the product requires every long operation
// to allow it.
//
// Restoring through the caller's context could not work: the context is
// cancelled, which is why the read stopped, and the statement that puts the
// path back is refused for the same reason. The connection then goes back to
// the pool still pointing at the schema that was being read, and the next
// person's unqualified names resolve in the wrong place — the pool runs no
// reset between callers, so closing the session does not clear it either.
//
// A unit test rather than one against a server, because what is being asserted
// is which context the statement is issued with. That is a property of this
// code, and reproducing it against a real server would mean cancelling at
// exactly the right moment and calling whatever happened the test.
func TestACancelledReadStillPutsTheSearchPathBack(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	server := &answers{
		rows: map[string][][]any{
			"pg_namespace":             existing(),
			"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
		},
		// Cancelled while the columns are being read, which is after the path
		// has been pointed at the schema and before the read could finish.
		cancelDuring: "pg_attribute",
		cancel:       cancel,
	}

	_, err := catalog.NewReader(server).Read(ctx, catalog.NewName("sales"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Read() = %v, want the cancellation", err)
	}

	if !server.restoredLive {
		t.Error("the search path was put back through the cancelled context, which cannot work")
	}
}

// A foreign key says which table it points at, as structure rather than only
// inside the text the server rendered.
//
// The writer acts on it: a key pointing at a table this version declines to
// write cannot be written either, and answering that by reading the definition
// would mean parsing SQL for something the catalog already knows. Every other
// kind of constraint points at nothing and says so.
func TestAForeignKeySaysWhatItPointsAt(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
		"pg_constraint": {
			{"orders", "orders_fk", "f", "FOREIGN KEY (c) REFERENCES other(id)", []string{"c"}, "other"},
			{"orders", "orders_pk", "p", "PRIMARY KEY (a)", []string{"a"}, nil},
		},
	}}

	schema, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	constraints := schema.Tables[0].Constraints
	if got := constraints[0].References.String(); got != "other" {
		t.Errorf("the foreign key points at %q, want other", got)
	}
	if got := constraints[1].References; got.Valid() {
		t.Errorf("the primary key points at %q, want nothing", got)
	}
}

// The cost of reading a schema does not grow with what is in it.
//
// This is the decision the performance budget rests on, and until now only the
// budget guarded it — badly, because a benchmark measures wall clock. A
// thousand round trips against a container on the same machine cost a fraction
// of a second and would have passed the five-second budget without anybody
// learning that the reader had started asking per object.
//
// So it is counted instead of timed. The same reader over a schema of one table
// and a schema of fifty asks exactly the same questions: a fixed number for the
// whole schema, whatever it holds. That is what ADR-0007 records as reading by
// whole schema, and what makes a thousand tables comfortable rather than tight.
func TestReadingCostsTheSameNumberOfQueriesWhateverTheSchemaHolds(t *testing.T) {
	t.Parallel()

	asked := func(tables int) int {
		listed := make([][]any, 0, tables)
		columns := make([][]any, 0, tables)

		for i := range tables {
			named := fmt.Sprintf("table_%03d", i)
			listed = append(listed, []any{named, "r", false, "p", false, false, nil, nil})
			columns = append(columns, []any{named, "id", 1, "int4", true, nil, "", "", nil})
		}

		recorder := &recordingQuerier{inner: &answers{rows: map[string][][]any{
			"pg_namespace":             existing(),
			"pg_class ARRAY['r', 'p']": listed,
			"pg_attribute":             columns,
		}}}

		if _, err := catalog.NewReader(recorder).Read(t.Context(), catalog.NewName("sales")); err != nil {
			t.Fatalf("Read() = %v", err)
		}

		return len(recorder.sql)
	}

	one, fifty := asked(1), asked(50)
	if one != fifty {
		t.Errorf("a schema of one table costs %d queries and one of fifty costs %d;"+
			" the reader is asking per object", one, fifty)
	}

	// And the number itself, not only that it does not grow. Equality alone
	// lets a new fixed query in without anybody noticing, and every one of them
	// is a round trip on every expansion of every schema.
	//
	// Eleven: three for the search path — what it was, pointing it, putting it
	// back — one asking whether the schema is there at all, and seven reading
	// the objects: tables, columns, constraints, indexes, sequences, views and
	// dependencies.
	const asks = 11

	if one != asks {
		t.Errorf("reading a schema costs %d queries, want %d — a query was added"+
			" or removed, which is a decision rather than a detail", one, asks)
	}
}

// A schema is read inside one view of the database, and the view is left
// afterwards.
//
// Eleven statements go into a schema. Without a snapshot each of them sees a
// different database, and the failure that produces is silent: a table listed by
// the first and dropped before the second comes back with no columns, no
// constraints and no indexes, and nothing reports it. The model is then
// indistinguishable from a table that lost everything, and the diff offers to
// put it back.
func TestASchemaIsReadInsideOneSnapshot(t *testing.T) {
	t.Parallel()

	server := &answers{rows: map[string][][]any{
		"pg_namespace":             existing(),
		"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
	}}

	if _, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales")); err != nil {
		t.Fatalf("Read() = %v", err)
	}

	if got := server.snapshots.Load(); got != 1 {
		t.Errorf("the read opened %d snapshots, want one", got)
	}
	if got := server.left.Load(); got != 1 {
		t.Errorf("the read left %d snapshots, want one", got)
	}
}

// A caller that already has a transaction keeps it.
//
// Its transaction is the one in force and it is not this package's to end —
// rolling it back at the end of a read would undo work the caller had done and
// not asked anybody to discard.
func TestAReadInsideSomebodyElsesTransactionLeavesItAlone(t *testing.T) {
	t.Parallel()

	server := &answers{
		inTransaction: true,
		rows: map[string][][]any{
			"pg_namespace":             existing(),
			"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
		},
	}

	if _, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales")); err != nil {
		t.Fatalf("Read() = %v", err)
	}

	if got := server.left.Load(); got != 0 {
		t.Errorf("the read ended somebody else's transaction %d times", got)
	}
}

// A server that will not give a snapshot fails the read.
//
// Reading anyway would answer a model that might be of no moment at all, and a
// model nobody can trust is worse than an error: the diff cannot tell one from
// a schema that really is in that state.
func TestAReadThatCannotGetASnapshotFails(t *testing.T) {
	t.Parallel()

	refused := errors.New("the server would not")
	server := &answers{
		snapshotFails: refused,
		rows:          map[string][][]any{"pg_namespace": existing()},
	}

	if _, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales")); !errors.Is(err, refused) {
		t.Errorf("Read() = %v, want the refusal", err)
	}
}

// A caller's transaction that cannot carry the read is refused, and the refusal
// says which of the two things is missing.
//
// The read is built on both. One view of the database, so that eleven
// statements describe one moment — without it a table dropped between two of
// them comes back empty and nothing reports it. And no writing, so that a
// function resolved under a search path pointed at somebody else's schema
// cannot do anything but read.
//
// An ordinary Begin gives neither. Accepting it unseen answered a model with
// the same shape and none of the guarantees, which nobody downstream can tell
// apart from a good one — verified against a server, where a write inside the
// transaction the reader was reading in succeeded.
func TestAReadRefusesATransactionThatCannotCarryIt(t *testing.T) {
	t.Parallel()

	for name, kind := range map[string]struct{ isolation, readOnly, says string }{
		"one that sees each statement differently": {"read committed", "on", "read committed"},
		"one that can write":                       {"repeatable read", "off", "must not"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			server := &answers{
				inTransaction: true,
				isolation:     kind.isolation,
				readOnly:      kind.readOnly,
				rows: map[string][][]any{
					"pg_namespace":             existing(),
					"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
				},
			}

			_, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
			if !errors.Is(err, catalog.ErrUnsuitableTransaction) {
				t.Fatalf("Read() = %v, want ErrUnsuitableTransaction", err)
			}
			if !strings.Contains(err.Error(), kind.says) {
				t.Errorf("Read() = %q, want it to say what is missing", err)
			}
		})
	}
}

// A transaction that cannot answer is not an unsuitable transaction.
//
// The difference is what the caller does next. Unsuitable means open the right
// kind or none at all; a transaction that will not answer means the session is
// finished and the recovery is a new one.
//
// They were the same error, and the way there is entirely inside what this
// branch already does: a rollback that does not land leaves the session holding
// a transaction pgx has already closed on a connection it has already killed,
// so BeginSnapshot answers ErrTransactionActive, this query fails, and every
// read afterwards said the open transaction could not carry it — about a
// session where the caller had opened none and could do nothing but close it.
func TestATransactionThatCannotAnswerIsNotAnUnsuitableOne(t *testing.T) {
	t.Parallel()

	dead := errors.New("conn closed")

	server := &answers{
		inTransaction: true,
		kindFails:     dead,
		rows:          map[string][][]any{"pg_namespace": existing()},
	}

	_, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))

	if !errors.Is(err, dead) {
		t.Fatalf("Read() = %v, want the failure that actually happened", err)
	}

	if errors.Is(err, catalog.ErrUnsuitableTransaction) {
		t.Errorf("Read() = %q, want it not to blame the kind of transaction", err)
	}
}

// A caller's transaction that does carry the read is used, and left alone.
//
// Serializable as well as repeatable read: it gives everything repeatable read
// gives and more, so insisting on the weaker of the two would refuse a caller
// who had been more careful.
func TestAReadUsesACallersSnapshotAndLeavesItAlone(t *testing.T) {
	t.Parallel()

	for _, isolation := range []string{"repeatable read", "serializable"} {
		t.Run(isolation, func(t *testing.T) {
			t.Parallel()

			server := &answers{
				inTransaction: true,
				isolation:     isolation,
				readOnly:      "on",
				rows: map[string][][]any{
					"pg_namespace":             existing(),
					"pg_class ARRAY['r', 'p']": {{"orders", "r", false, "p", false, false, nil, nil}},
				},
			}

			if _, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales")); err != nil {
				t.Fatalf("Read() = %v", err)
			}

			if got := server.left.Load(); got != 0 {
				t.Errorf("the read ended somebody else's transaction %d times", got)
			}
		})
	}
}

// A scoping call that fails still puts the path back.
//
// The failure may be in reading the answer to a statement the server already
// applied: a cancelled context and a connection dropped mid-answer both look
// like this from here, and only one of them leaves the path where it was. The
// read gave up without trying, which left the session pointing at the schema it
// had been asked to read — the state the whole of scopeTo exists to avoid, and
// reached through the one path that skipped it.
func TestAFailedScopingStillPutsTheSearchPathBack(t *testing.T) {
	t.Parallel()

	refused := errors.New("connection reset")

	server := &answers{
		rows:         map[string][][]any{"pg_namespace": existing()},
		scopingFails: refused,
	}

	_, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if !errors.Is(err, refused) {
		t.Fatalf("Read() = %v, want the failure that actually happened", err)
	}

	if got := server.restored.Load(); got != 1 {
		t.Errorf("the reader put the search path back %d times, want once", got)
	}
}

// A read that fails reports what failed, and not a second time about the search
// path.
//
// Once a statement in the transaction has failed the server refuses everything
// until somebody ends it, including the statement that puts the path back. That
// is not news about the connection — the rollback that follows puts the path
// back anyway, because a SET inside a transaction goes back with it — and
// reporting it put an alarm on top of every ordinary failure. The alarm means a
// connection resolving the next caller's names in the wrong schema, so it has to
// mean only that.
func TestAFailedReadDoesNotAlsoComplainAboutTheSearchPath(t *testing.T) {
	t.Parallel()

	broken := errors.New("column does not exist")

	// The error the driver actually hands up, built the way the driver builds
	// it. A version of this test wrote the message itself, which meant it
	// proved the reader could read a string this test had written.
	aborted := &driver.Failure{
		Class:    driver.FailureUnknown,
		SQLState: "25P02",
		Err:      errors.New("current transaction is aborted"),
	}

	server := &answers{
		rows: map[string][][]any{"pg_namespace": existing()},
		err:  map[string]error{"pg_class": broken},
		// What the server says to the restore once the read has failed.
		restoringFails: aborted,
	}

	_, err := catalog.NewReader(server).Read(t.Context(), catalog.NewName("sales"))
	if !errors.Is(err, broken) {
		t.Fatalf("Read() = %v, want the failure that actually happened", err)
	}
	if strings.Contains(err.Error(), "search path") {
		t.Errorf("Read() = %q, want nothing about the search path", err)
	}
}
