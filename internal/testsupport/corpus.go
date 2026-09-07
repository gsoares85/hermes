//go:build integration

package testsupport

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// The corpus of pathological schemas: the fixture the catalog, the DDL writer
// and the diff are all measured against.
//
// It is not a happy path with a few extras. Every entry is here because it has
// broken a tool that reads catalogs, and because a reader that handles the
// ordinary case and not these is a reader whose diff produces noise on the
// first real database it meets. The testing strategy lists what it has to
// contain; this is that list, made.
//
// It grows with the phases that read it. A phase that adds a query adds the
// objects that query is about, so that no fixture is written for something
// nothing yet looks at — a fixture nobody reads is a fixture nobody notices is
// wrong.

// corpusSequence keeps two calls from colliding. The container is shared by
// every test in the binary and outlives all of them, so a fixed schema name is
// a schema that is already there the second time anything creates it.
var corpusSequence atomic.Uint64

// Corpus creates the pathological schema and answers its name.
//
// A schema of its own per call rather than one shared: creating it is DDL on a
// container that is already running, which costs milliseconds, and sharing it
// would make one test's failure show up in another's.
func Corpus(tb testing.TB, instance *Instance) string {
	tb.Helper()

	schema := CorpusTarget(tb, instance)

	// Not dropped when the test ends, and that is not an omission. Exec runs
	// through the context of the test, which is already cancelled by the time a
	// cleanup runs — a drop registered here fails every time and reports a
	// second problem on top of whatever the test found. The container is the
	// cleanup: it is thrown away at the end of the run, and the name carries a
	// counter so no two calls collide inside one.
	run(tb, instance, schema, corpusStatements)

	return schema
}

// CorpusTarget creates a schema holding what the corpus needs and the model
// does not carry, and nothing else.
//
// It is what a round trip writes into. The DDL written from a model can only
// build what the model holds, and the model deliberately does not hold types:
// an enum, a domain and a composite are SYN-04, out of scope for this version
// and named as not compared rather than pretended about. So the target is given
// them, and what the round trip then proves is that everything the model does
// hold survives being written and read again — which is the property, rather
// than a claim that the writer can build a schema out of nothing.
func CorpusTarget(tb testing.TB, instance *Instance) string {
	tb.Helper()

	schema := fmt.Sprintf("corpus_%d", corpusSequence.Add(1))

	installExtensions(tb, instance)
	instance.Exec(tb, "CREATE SCHEMA "+schema)
	run(tb, instance, schema, corpusPrerequisites)

	return schema
}

// run applies statements to a schema, putting its name where the token is.
func run(tb testing.TB, instance *Instance, schema string, statements []string) {
	tb.Helper()

	for _, statement := range statements {
		instance.Exec(tb, strings.ReplaceAll(statement, schemaToken, schema))
	}
}

// Extensions belong to the database rather than to a schema, so they are
// installed once per container instead of once per corpus.
//
// It has to be serialised, and IF NOT EXISTS is not enough: two tests building
// their own corpus at the same time both find it missing and both try to create
// it, and the second one fails on the unique index over the extension name.
// That is a race in the fixture that reads as a failure of the code under test,
// which is the worst kind of flake to be handed.
var (
	extensionsOnce  sync.Mutex
	extensionsBuilt = map[string]bool{}
)

func installExtensions(tb testing.TB, instance *Instance) {
	tb.Helper()

	extensionsOnce.Lock()
	defer extensionsOnce.Unlock()

	if extensionsBuilt[instance.Version] {
		return
	}

	// btree_gist is what lets an exclusion constraint mix an equality on a
	// scalar with an overlap on a range, which is the shape the corpus needs
	// and the one information_schema cannot see at all.
	instance.Exec(tb, "CREATE EXTENSION IF NOT EXISTS btree_gist")

	extensionsBuilt[instance.Version] = true
}

// schemaToken is what stands in for the schema in the statements below.
//
// A token replaced by name rather than a format verb: several of these mention
// the schema more than once, and a count that drifts from the arguments is a
// fixture that fails to build for a reason that has nothing to do with what it
// was written to prove.
const schemaToken = "{schema}"

// The statements that create what the corpus needs and the model does not
// carry.
//
// Types of their own — an enum, a domain, a composite — which the columns below
// use and which this version does not compare. They are split out from the rest
// because the round trip has to be able to build the target without them being
// part of what is written: a model that does not hold a type cannot write one,
// and a fixture that mixed the two would make the round trip prove something
// weaker than it claims.
var corpusPrerequisites = []string{
	`CREATE TYPE {schema}.mood AS ENUM ('sad', 'ok', 'happy')`,
	`CREATE DOMAIN {schema}.positive AS integer CHECK (VALUE > 0)`,
	`CREATE TYPE {schema}.pair AS (first integer, second text)`,
}

// The statements, each naming the schema through schemaToken.
//
// Ordered so that what a later one depends on already exists, which is the same
// order the dependency phase will later have to work out for itself.
var corpusStatements = []string{
	// Functions that shadow the ones the reader calls, which is the shape of
	// CVE-2018-1058 and the reason every call in those queries names
	// pg_catalog.
	//
	// The reader points the search path at the schema it is reading, so
	// anything declared here is on the path while it reads. Shadowing is not
	// blocked by pg_catalog being implicitly first: that only settles two
	// candidates with identical signatures, and these are deliberately exact
	// matches for the argument types the reader passes, while the catalog's own
	// are polymorphic. An exact match wins over a polymorphic one wherever it
	// sits on the path.
	//
	// So they are in the corpus rather than in a test of their own: every
	// integration test reads this schema, and a call that loses its
	// qualification makes all of them fail rather than one nobody ran.
	//
	// The bodies answer a wrong value instead of doing damage. What is being
	// proved is which function the server chose, and a fixture that granted
	// itself rights would be a fixture nobody wants in their test database.
	`CREATE FUNCTION {schema}.unnest(smallint[]) RETURNS SETOF smallint
		LANGUAGE sql IMMUTABLE AS $$ SELECT 666::smallint $$`,
	`CREATE FUNCTION {schema}.unnest(int2vector) RETURNS SETOF smallint
		LANGUAGE sql IMMUTABLE AS $$ SELECT 666::smallint $$`,
	`CREATE FUNCTION {schema}.hijack(name[], name) RETURNS name[]
		LANGUAGE sql IMMUTABLE AS $$ SELECT ARRAY['hijacked'::name] $$`,
	`CREATE AGGREGATE {schema}.array_agg(name) (
		SFUNC = {schema}.hijack, STYPE = name[], INITCOND = '{}')`,

	// Names that need quoting, in three ways that each broke something once:
	// upper case, which folds if it is not quoted; a space, which ends the
	// identifier; and an accent, which is more than one byte and sorts
	// differently under every locale.
	`CREATE TABLE {schema}."Customer" (
		id integer PRIMARY KEY,
		"Full Name" text NOT NULL,
		"endereço" text
	)`,

	// Every shape a column can have that the model has a field for. The
	// defaults are deliberately of different kinds — a literal, a cast, a
	// function call — because they render differently and the canonical form
	// has to survive all three.
	`CREATE TABLE {schema}.column_shapes (
		id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		by_default integer GENERATED BY DEFAULT AS IDENTITY,
		plain_default integer DEFAULT 42,
		cast_default text DEFAULT 'x'::text,
		called_default timestamp with time zone DEFAULT now(),
		generated_column integer GENERATED ALWAYS AS (plain_default * 2) STORED,
		collated text COLLATE "C",
		nullable text,
		not_nullable text NOT NULL,
		typed {schema}.mood,
		domained {schema}.positive,
		composite {schema}.pair,
		numbers integer[],
		matrix integer[][]
	)`,

	// The spellings of one type. Every one of these is an alias, and a reader
	// that stored what was written would report six type changes between this
	// table and one declared the long way.
	`CREATE TABLE {schema}.type_aliases (
		a int4,
		b int,
		c int2,
		d int8,
		e bool,
		f float4,
		g float8,
		h varchar(50),
		i decimal(10,2),
		j timestamptz,
		k timestamp,
		l timetz,
		m time,
		n char(3),
		o serial,
		p bpchar,
		q bpchar(3)
	)`,

	// Inheritance, which is not partitioning and is read as its own thing.
	`CREATE TABLE {schema}.parent (id integer PRIMARY KEY, common text)`,
	`CREATE TABLE {schema}.child (extra text) INHERITS ({schema}.parent)`,

	// Declarative partitioning. The partitions are tables in their own right,
	// which is what this proves the reader survives.
	`CREATE TABLE {schema}.measurements (
		id integer NOT NULL,
		taken date NOT NULL
	) PARTITION BY RANGE (taken)`,
	`CREATE TABLE {schema}.measurements_2026 PARTITION OF {schema}.measurements
		FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')`,

	// A dropped column. PostgreSQL leaves the entry in pg_attribute so the row
	// layout does not move, and a reader that does not skip it puts a column
	// called ........pg.dropped.2........ in the model.
	`CREATE TABLE {schema}.with_a_hole (id integer, gone text, kept text)`,
	`ALTER TABLE {schema}.with_a_hole DROP COLUMN gone`,

	// A table with no columns at all is legal, and it is the shape that finds
	// a reader which assumes every table joins to at least one row.
	`CREATE TABLE {schema}.empty_table ()`,

	// Every kind of constraint the model compares, on one table, so that a
	// kind lost between the query and the model is one failing assertion
	// rather than a schema that merely looks a little smaller.
	//
	// The keys are deliberately over more than one column, because the order
	// of a key is part of it: (a, b) and (b, a) are different constraints, and
	// a reader that unnests without keeping the order gets it right about half
	// the time.
	`CREATE TABLE {schema}.constrained (
		first integer NOT NULL,
		second integer NOT NULL,
		label text,
		amount numeric(10,2) CHECK (amount > 0),
		CONSTRAINT constrained_pk PRIMARY KEY (first, second),
		CONSTRAINT constrained_unique UNIQUE (second, label),
		CONSTRAINT constrained_check CHECK (char_length(label) < 100)
	)`,

	// A foreign key that points at itself, which is legal and which a
	// topological order has to survive.
	`CREATE TABLE {schema}.employee (
		id integer PRIMARY KEY,
		manager integer REFERENCES {schema}.employee (id)
	)`,

	// Foreign keys in a circle. PostgreSQL allows it, no CREATE TABLE order
	// produces it, and the only way to write it is to add one of them
	// afterwards — which is what the DDL phase will have to work out for
	// itself.
	`CREATE TABLE {schema}.circle_a (id integer PRIMARY KEY, b_id integer)`,
	`CREATE TABLE {schema}.circle_b (id integer PRIMARY KEY, a_id integer)`,
	`ALTER TABLE {schema}.circle_a ADD CONSTRAINT circle_a_to_b
		FOREIGN KEY (b_id) REFERENCES {schema}.circle_b (id) ON DELETE SET NULL`,
	`ALTER TABLE {schema}.circle_b ADD CONSTRAINT circle_b_to_a
		FOREIGN KEY (a_id) REFERENCES {schema}.circle_a (id) ON DELETE CASCADE`,

	// An exclusion constraint, which is the thing information_schema cannot
	// see at all and one of the reasons ADR-0007 reads the catalog instead.
	// The extension it needs is installed once per container, above.
	`CREATE TABLE {schema}.booking (
		room integer,
		during tsrange,
		EXCLUDE USING gist (room WITH =, during WITH &&)
	)`,

	// The indexes that are not a plain list of columns. Every one of these is
	// invisible to information_schema, and each has broken a diff somewhere.
	`CREATE TABLE {schema}.indexed (
		id integer PRIMARY KEY,
		email text,
		status text,
		created timestamp with time zone,
		payload text
	)`,
	`CREATE INDEX indexed_partial ON {schema}.indexed (email) WHERE status = 'active'`,
	`CREATE INDEX indexed_expression ON {schema}.indexed (lower(email))`,
	`CREATE UNIQUE INDEX indexed_unique ON {schema}.indexed (email, status)`,
	`CREATE INDEX indexed_include ON {schema}.indexed (status) INCLUDE (payload)`,
	`CREATE INDEX indexed_descending ON {schema}.indexed (created DESC NULLS LAST)`,

	// A plain unique index that a foreign key points at.
	//
	// PostgreSQL fills a foreign key's conindid with the index of the table it
	// references, so a reader that treats conindid as "this index belongs to a
	// constraint" loses this index — and then the generated script dies on the
	// ALTER TABLE that adds the key, because the unique index it needs was
	// never created. Nothing else in the corpus has this shape: every other
	// unique index here either backs a constraint or is pointed at by nothing.
	`CREATE TABLE {schema}.referenced (code integer NOT NULL, label text)`,
	`CREATE UNIQUE INDEX referenced_code ON {schema}.referenced (code)`,
	`CREATE TABLE {schema}.referring (
		id integer PRIMARY KEY,
		code integer REFERENCES {schema}.referenced (code)
	)`,

	// A sequence nobody owns, with every parameter declared away from its
	// default. A copy made with the defaults hands out different numbers from
	// the original, which is a data fault produced by a copy of the structure.
	`CREATE SEQUENCE {schema}.standalone_counter
		AS smallint
		START WITH 100
		INCREMENT BY 5
		MINVALUE 10
		MAXVALUE 30000
		CACHE 20
		CYCLE`,

	// A sequence a column owns, declared the long way so that OWNED BY is
	// explicit rather than something serial arranged behind the scenes — the
	// serial column of type_aliases already covers that shape, and the identity
	// columns of column_shapes cover the one the server owns internally.
	`CREATE TABLE {schema}.counted (id integer, label text)`,
	`CREATE SEQUENCE {schema}.counted_id_seq OWNED BY {schema}.counted.id`,
	`ALTER TABLE {schema}.counted
		ALTER COLUMN id SET DEFAULT nextval('{schema}.counted_id_seq')`,

	// Views, including the shape a dependency order will have to work out for
	// itself: one view standing on another.
	`CREATE VIEW {schema}.active_orders AS
		SELECT id, email, status FROM {schema}.indexed WHERE status = 'active'`,
	`CREATE VIEW {schema}.active_domains AS
		SELECT lower(split_part(email, '@', 2)) AS domain FROM {schema}.active_orders`,

	// WITH CHECK OPTION is not part of the query the server renders back — it
	// is stored beside the view — so a reader that asks only for the definition
	// loses the clause that decides whether a write through the view is
	// refused, and the copy at the other end accepts rows the original rejects.
	`CREATE VIEW {schema}.checked_orders AS
		SELECT id, email, status FROM {schema}.indexed WHERE status = 'active'
		WITH CASCADED CHECK OPTION`,

	// A view standing on something outside the schema. The model is of one
	// schema and cannot order an object it does not hold, so the dependency is
	// left out of the graph — and leaving it out has to be that and not a
	// reader deciding the catalog contradicted itself, which is what a schema
	// with a foot in another one would otherwise look like.
	`CREATE VIEW {schema}.catalog_peek AS
		SELECT oid, relname FROM pg_catalog.pg_class`,

	// A view whose query refers to itself, through a recursive CTE. The
	// self-reference is legal and resolves inside the query, so a reader that
	// mistakes it for a dependency on the view finds a cycle that is not there.
	`CREATE VIEW {schema}.reporting_line AS
		WITH RECURSIVE reports AS (
			SELECT id, manager FROM {schema}.employee WHERE manager IS NULL
			UNION ALL
			SELECT e.id, e.manager
			FROM {schema}.employee e JOIN reports r ON e.manager = r.id
		)
		SELECT id, manager FROM reports`,
}
