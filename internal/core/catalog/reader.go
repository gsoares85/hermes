package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gsoares85/hermes/internal/driver"
)

// ErrSchemaNotFound is a schema the server does not have.
//
// It is a sentinel because the layer above answers it differently from an empty
// schema, and the difference is not cosmetic: "this database has no such
// schema" and "this schema has nothing in it" send a person to two different
// places, and a diff whose target is missing must say so rather than compare
// against emptiness and offer to create the world.
var ErrSchemaNotFound = errors.New("no such schema")

// ErrInconsistentCatalog is the catalog answering two questions in ways that
// cannot both be true — a column of a table that was not listed.
//
// It should not happen. Two queries filtered the same way should see the same
// tables, and when they do not, something is wrong with the queries rather than
// with the server. Reporting it beats dropping the row, which would hide the
// mistake behind a model that merely looks a little smaller than it should.
var ErrInconsistentCatalog = errors.New("the catalog answered inconsistently")

// Querier is what reading a schema needs from a connection.
//
// It is declared here, in the package that consumes it, rather than in the one
// that implements it — so this package can be tested against a double and never
// needs a server to prove that it assembles a model correctly. What satisfies
// it is a driver.Session, which the command that wires the application together
// hands down.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) driver.Rows

	// BeginSnapshot and Rollback are here because a schema is read across
	// eleven statements and has to be the schema of one moment. See snapshot.
	BeginSnapshot(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Reader reads a schema out of pg_catalog.
type Reader struct{ server Querier }

// NewReader builds a reader over a connection.
func NewReader(server Querier) *Reader { return &Reader{server: server} }

// Read answers the whole schema.
//
// It is a fixed number of queries whatever the schema holds — one per kind of
// object, each filtered to the schema and each returning every row of its kind.
// That is the decision ADR-0007 records as "by whole schema", and it is what
// makes the budget of a thousand tables comfortable rather than tight: asking
// per object would be a thousand round trips before anything is assembled.
//
// The name reaches the server as a parameter and never as text spliced into a
// query. It arrives from a person, and a schema name is exactly the sort of
// thing that carries a quote.
//
// For the length of the read the session's search path names this schema and
// nothing else, which is what makes every expression the server renders
// independent of what the schema is called. See expr.go for why that is the
// only place it can be decided. The path is put back afterwards, and a failure
// to put it back is reported alongside whatever the read answered.
func (r *Reader) Read(ctx context.Context, schema Name) (read Schema, err error) {
	if !schema.Valid() {
		return Schema{}, fmt.Errorf("%w: %q cannot name a schema", ErrSchemaNotFound, schema)
	}

	release, err := r.snapshot(ctx)
	if err != nil {
		return Schema{}, err
	}

	// The same rule as the restore below, and for the same reason: there is one
	// answer here. A model read perfectly well on a connection whose transaction
	// would not close is still an answer nobody can act on, because nobody knows
	// what that connection is in the middle of. Leaving both would hand the
	// caller a choice it has no way to make, and the two defers used to disagree
	// about which way to make it.
	defer func() {
		if failed := release(); failed != nil {
			read, err = Schema{}, errors.Join(err, failed)
		}
	}()

	found, err := r.exists(ctx, schema)
	if err != nil {
		return Schema{}, err
	}
	if !found {
		return Schema{}, fmt.Errorf("%w: %s", ErrSchemaNotFound, schema)
	}

	restore, err := r.scopeTo(ctx, schema)
	if err != nil {
		return Schema{}, err
	}

	// A model is answered only when the path went back. Handing back both a
	// schema and an error leaves the caller a choice it has no way to make
	// well: the model is fine, and the session it was read on is not — so a
	// caller that treats the error as a warning goes on using a connection
	// that resolves names in the wrong schema. There is one answer here, and
	// when the session is broken the answer is the error.
	defer func() {
		if failed := restore(); failed != nil {
			read, err = Schema{}, errors.Join(err, failed)
		}
	}()

	read = Schema{Name: schema}

	tables, order, err := r.tables(ctx, schema)
	if err != nil {
		return Schema{}, err
	}

	// Each of these fills the tables it belongs to, and each is one query for
	// the whole schema. A failure in any of them fails the read: answering a
	// partial schema would be worse than answering nothing, because a diff
	// would compare against it and offer to drop whatever the failed query
	// would have listed.
	for _, fill := range []func(context.Context, Name, map[string]*Table) error{
		r.columns,
		r.constraints,
		r.indexes,
	} {
		if err = fill(ctx, schema, tables); err != nil {
			return Schema{}, err
		}
	}

	for _, name := range order {
		read.Tables = append(read.Tables, *tables[name])
	}

	// The objects of the schema that hang on nothing. They are read after the
	// tables rather than beside them only because the queries run one at a time
	// on one connection; nothing about them depends on what the tables said.
	if read.Sequences, err = r.sequences(ctx, schema); err != nil {
		return Schema{}, err
	}

	if read.Views, err = r.views(ctx, schema); err != nil {
		return Schema{}, err
	}

	// Last, because it is the only read that is about the objects rather than
	// about one of them: an edge may only name something the reads above
	// listed, and checking that is what keeps a hole in the order from
	// reaching the DDL writer.
	if read.Dependencies, err = r.dependencies(ctx, schema, objectsOf(read)); err != nil {
		return Schema{}, err
	}

	// Sorted before it is handed over, so that no caller has to know the reader
	// assembled it out of maps. See Sort.
	read.Sort()

	return read, nil
}

// snapshot puts the read inside one view of the database and answers how to
// leave it.
//
// Eleven statements go into a schema, and without this each of them sees a
// different database. The failure that produces is not an error: a table listed
// by the first statement and dropped before the second comes back with no
// columns, no constraints and no indexes, and nothing says so — a model
// indistinguishable from a table that lost everything, which the diff would
// then offer to restore. A table created between the two is the other half, and
// that one at least reports, though it reports as though this package had got
// its queries wrong.
//
// A caller that already has a transaction keeps it, and is asked what kind it
// is before the read goes on. Its transaction is the one in force and not this
// function's to end — but it is also the one this read depends on, and accepting
// it unseen threw away both properties in silence: a caller that had opened an
// ordinary transaction got a complete model read across eleven views of the
// database, with the writes this read declares it does not make no longer
// refused by anything. Verified against a server, which is where "silence" was
// the word for it.
//
// Leaving is a rollback rather than a commit because nothing was written; the
// transaction is read-only and the server would refuse anything else.
func (r *Reader) snapshot(ctx context.Context) (func() error, error) {
	opened := r.server.BeginSnapshot(ctx)

	switch {
	case errors.Is(opened, driver.ErrTransactionActive):
		if unsuitable := r.suitable(ctx); unsuitable != nil {
			return nil, unsuitable
		}

		return func() error { return nil }, nil
	case opened != nil:
		return nil, fmt.Errorf("opening a snapshot to read from: %w", opened)
	}

	return func() error {
		// Its own context, for the reason the search path has one: the read
		// ending early is when leaving matters, and the commonest way for it to
		// end early is a cancellation that would refuse this statement too —
		// leaving the transaction open on a connection going back to the pool.
		leave, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
		defer cancel()

		if err := r.server.Rollback(leave); err != nil {
			return fmt.Errorf("leaving the snapshot: %w", err)
		}

		return nil
	}, nil
}

// ErrUnsuitableTransaction is a caller's transaction that cannot carry a read.
//
// Separate from ErrSchemaNotFound and from the catalog being inconsistent
// because it is neither: nothing is wrong with the schema or the server, and
// the caller can fix it by opening the right kind of transaction or none at
// all.
var ErrUnsuitableTransaction = errors.New("the open transaction cannot carry a read")

// transactionKind asks what the transaction in force actually is.
const transactionKind = `SELECT pg_catalog.current_setting('transaction_isolation'),
       pg_catalog.current_setting('transaction_read_only')`

// suitable refuses a caller's transaction that does not give what the read
// needs.
//
// Two properties, and the read is built on both. One view of the database, so
// that eleven statements describe one moment rather than eleven — without it a
// table dropped between two of them comes back with nothing in it and no error.
// And no writing, so that a function resolved under a search path pointed at
// somebody else's schema cannot do anything but read.
//
// Both are given by BeginSnapshot and neither is given by an ordinary Begin.
// Reading anyway would answer a model with the same shape and none of the
// guarantees, which nobody downstream can tell apart — so it is refused, and
// the caller is told which of the two is missing.
func (r *Reader) suitable(ctx context.Context) error {
	rows := r.server.Query(ctx, transactionKind)
	defer rows.Close()

	if !rows.Next() {
		return fmt.Errorf("%w: the server did not say what kind it is: %w",
			ErrUnsuitableTransaction, rows.Err())
	}

	var isolation, readOnly string
	if err := rows.Scan(&isolation, &readOnly); err != nil {
		return fmt.Errorf("%w: reading what kind it is: %w", ErrUnsuitableTransaction, err)
	}

	// Serializable gives everything repeatable read gives and more, so it is
	// accepted rather than insisted against.
	if isolation != "repeatable read" && isolation != "serializable" {
		return fmt.Errorf("%w: it is %s, so each statement would see a different"+
			" database and a schema read across several would describe none of them",
			ErrUnsuitableTransaction, isolation)
	}

	if readOnly != "on" {
		return fmt.Errorf("%w: it can write, and a read of a schema on the search path"+
			" of that schema's owner must not", ErrUnsuitableTransaction)
	}

	return nil
}

// schemaExists asks whether the namespace is there at all.
//
// A query of its own rather than inferring it from an empty table list, because
// the two are different answers and only one of them is a fault.
const schemaExists = `SELECT n.nspname
	FROM pg_catalog.pg_namespace n
	WHERE n.nspname OPERATOR(pg_catalog.=) $1`

func (r *Reader) exists(ctx context.Context, schema Name) (bool, error) {
	rows := r.server.Query(ctx, schemaExists, schema.String())
	defer rows.Close()

	found := rows.Next()
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("looking for the schema %s: %w", schema, err)
	}

	return found, nil
}

// listTables reads the tables of a schema, what each inherits, and whether it
// takes part in partitioning.
//
// relkind 'r' is an ordinary table and 'p' a partitioned one. A partitioned
// table is a table, and leaving it out would make the reader silently miss it.
// Both facts about partitioning are read rather than modelled: this version
// does not compare partitioning, and what is not compared has to be named as
// not compared instead of quietly written as an ordinary table. See
// Table.Partitioned.
//
// The parents come back through pg_inherits ordered by inhseqno, which is the
// order they were declared in and therefore the order the inherited columns
// appear in. Sorting them would be wrong, which is why this is the one list in
// the model Sort leaves alone. A parent in another schema is left out: the
// model is of one schema and cannot address outside it, the same boundary the
// dependency graph draws.
//
// relpersistence 'u' is an unlogged table, which is a property of the table and
// not a tuning knob: a copy made logged has durability the original never had,
// and a copy made unlogged throws away rows the first time the server stops
// badly. 't' is a temporary table, which belongs to one session and is not an
// object anybody would compare — those are excluded by the schema filter
// already, since they live in a namespace of their own.
//
// relrowsecurity and relforcerowsecurity are read because a copy without them
// shows every row the original hid, and nothing about the copy would say a
// control had been dropped. The policies themselves are not compared by this
// version, so the writer declines a table that has row security rather than
// writing one that reveals more than what it was copied from.
//
// reloptions are the storage parameters somebody set — fillfactor, autovacuum
// thresholds — and a copy without them behaves differently under load than the
// thing it was copied from. What is not read is the tablespace: it names an
// object of the server rather than of the schema, and a copy pointed at a
// tablespace the other server has never heard of fails to build at all. It is
// not compared, and this is where that is written down.
//
// Everything else pg_class holds — indexes, sequences, views, TOAST tables — is
// either read by a query of its own or is not an object of this model.
const listTables = `SELECT c.relname,
	       c.relkind,
	       c.relispartition,
	       c.relpersistence,
	       c.relrowsecurity,
	       c.relforcerowsecurity,
	       c.reloptions,
	       parents.inherits
	FROM pg_catalog.pg_class c
	JOIN pg_catalog.pg_namespace n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace
	LEFT JOIN LATERAL (
	        SELECT pg_catalog.array_agg(p.relname ORDER BY i.inhseqno) AS inherits
	        FROM pg_catalog.pg_inherits i
	        JOIN pg_catalog.pg_class p ON p.oid OPERATOR(pg_catalog.=) i.inhparent
	        JOIN pg_catalog.pg_namespace pn ON pn.oid OPERATOR(pg_catalog.=) p.relnamespace
	        WHERE i.inhrelid OPERATOR(pg_catalog.=) c.oid AND pn.nspname OPERATOR(pg_catalog.=) n.nspname
	     ) parents ON true
	WHERE n.nspname OPERATOR(pg_catalog.=) $1 AND c.relkind OPERATOR(pg_catalog.=) ANY (ARRAY['r', 'p'])`

// tables answers the tables of the schema, and the order the server listed
// them in — which the caller uses only to build the slice, because Sort decides
// the order the model has.
func (r *Reader) tables(ctx context.Context, schema Name) (map[string]*Table, []string, error) {
	rows := r.server.Query(ctx, listTables, schema.String())
	defer rows.Close()

	tables := map[string]*Table{}

	var order []string

	for rows.Next() {
		var (
			name, relkind, persistence  string
			partition, security, forced bool
			options, inherits           []string
		)

		if err := rows.Scan(&name, &relkind, &partition, &persistence,
			&security, &forced, &options, &inherits); err != nil {
			return nil, nil, fmt.Errorf("reading a table of %s: %w", schema, err)
		}

		tables[name] = &Table{
			Name:        NewName(name),
			Inherits:    namesOf(inherits),
			Partitioned: relkind == "p",
			Partition:   partition,
			Unlogged:    persistence == "u",
			RowSecurity: security,
			Forced:      forced,
			Options:     options,
		}
		order = append(order, name)
	}

	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("listing the tables of %s: %w", schema, err)
	}

	return tables, order, nil
}

// listColumns reads every column of every table in the schema, in one query.
//
// format_type is what makes the type the server's own canonical spelling rather
// than the one the column was declared with, and pg_get_expr does the same for
// the default: both are rendered from the parse tree, so two columns written
// differently and meaning the same come back the same.
//
// attnum > 0 skips the system columns, which belong to the storage rather than
// to the table anybody declared. attisdropped skips a column that was dropped:
// PostgreSQL leaves the entry in place so the row layout does not move, and
// reading it would put a column named ........pg.dropped.3........ in the model.
//
// The collation is joined rather than assumed. A column of a collatable type
// always has one, and the name of it is what the diff compares — leaving it
// empty for the common case would make every column that does declare one look
// different from every column that does not.
const listColumns = `SELECT c.relname,
	       a.attname,
	       a.attnum,
	       pg_catalog.format_type(a.atttypid, a.atttypmod),
	       a.attnotnull,
	       pg_catalog.pg_get_expr(d.adbin, d.adrelid),
	       a.attidentity,
	       a.attgenerated,
	       co.collname
	FROM pg_catalog.pg_attribute a
	JOIN pg_catalog.pg_class c ON c.oid OPERATOR(pg_catalog.=) a.attrelid
	JOIN pg_catalog.pg_namespace n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace
	LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid OPERATOR(pg_catalog.=) a.attrelid AND d.adnum OPERATOR(pg_catalog.=) a.attnum
	LEFT JOIN pg_catalog.pg_collation co ON co.oid OPERATOR(pg_catalog.=) a.attcollation
	WHERE n.nspname OPERATOR(pg_catalog.=) $1
	  AND c.relkind OPERATOR(pg_catalog.=) ANY (ARRAY['r', 'p'])
	  AND a.attnum OPERATOR(pg_catalog.>) 0
	  AND NOT a.attisdropped`

func (r *Reader) columns(ctx context.Context, schema Name, tables map[string]*Table) error {
	rows := r.server.Query(ctx, listColumns, schema.String())
	defer rows.Close()

	for rows.Next() {
		var (
			table, name         string
			position            int
			typeName            string
			notNull             bool
			def, collation      *string
			identity, generated string
		)

		if err := rows.Scan(&table, &name, &position, &typeName, &notNull,
			&def, &identity, &generated, &collation); err != nil {
			return fmt.Errorf("reading a column of %s: %w", schema, err)
		}

		owner, known := tables[table]
		if !known {
			return fmt.Errorf("%w: %s.%s has the column %s and was not listed as a table",
				ErrInconsistentCatalog, schema, table, name)
		}

		owner.Columns = append(owner.Columns, Column{
			Name:      NewName(name),
			Position:  position,
			Type:      NewTypeName(unqualify(typeName, schema)),
			NotNull:   notNull,
			Default:   text(def),
			Identity:  identityOf(identity),
			Generated: generatedOf(generated),
			Collation: NewName(text(collation)),
		})
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("listing the columns of %s: %w", schema, err)
	}

	return nil
}

// unqualify drops the schema from a type that lives in the schema being read.
//
// format_type qualifies a type that is not on the search path, so a column of
// an enum declared beside it comes back as sales.mood rather than as mood. The
// model is of one schema, and a type inside it is part of that schema — so
// storing the qualifier would make the same schema compare unequal to itself
// the moment it is read under another name, which is exactly what a diff
// between an environment and its copy does. Two schemas that are structurally
// the same would report a type change on every column that uses a type of
// their own.
//
// The read now points the path at the schema, so the server leaves the
// qualifier out on its own and this rarely has anything to do. It stays as the
// net under that, the way the alias table is the net under format_type: the
// type is the most compared field in the model, and six lines are cheap
// insurance for the one server that answers differently.
//
// A qualifier naming a different schema stays. That one is a real reference to
// somewhere else, and a schema pointing at public.mood is genuinely different
// from one pointing at its own.
//
// The quoted form is stripped too, because format_type quotes a schema whose
// name needs it, and the unquoted comparison would then miss.
func unqualify(typeName string, schema Name) string {
	for _, prefix := range []string{schema.String() + ".", `"` + schema.String() + `".`} {
		if rest, found := strings.CutPrefix(typeName, prefix); found {
			return rest
		}
	}

	return typeName
}

// text reads a column the catalog can answer NULL for. A column with no
// default has no default, which is the empty string here rather than a pointer
// every caller would have to check.
func text(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

// identityOf spells out what attidentity abbreviates. The catalog stores a
// single character, and a model that kept it would put 'a' and 'd' in front of
// whoever reads a diff.
func identityOf(stored string) string {
	switch stored {
	case "a":
		return "always"
	case "d":
		return "by default"
	default:
		return ""
	}
}

// generatedOf spells out what attgenerated abbreviates, for the same reason.
// The catalog has held only 's' since generated columns arrived; anything else
// is a form this build does not know, and naming it is better than dropping it.
//
// A column that is not generated holds a NUL rather than nothing: attgenerated
// is a "char", which is one byte and is zero when there is nothing to say. It
// arrives here as "\x00", and a check for the empty string alone lets that
// through — which puts a NUL in the model of every ordinary column, compares
// equal to itself so no reading notices, and reaches daylight only when the DDL
// writer puts GENERATED ALWAYS AS () and a zero byte into a statement.
func generatedOf(stored string) string {
	switch stored {
	case "", "\x00":
		return ""
	case "s":
		return "stored"
	default:
		return stored
	}
}
