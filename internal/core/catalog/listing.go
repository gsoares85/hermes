package catalog

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/gsoares85/hermes/internal/driver"
)

// Asker is what a listing needs from a connection, and it is deliberately less
// than what reading a schema needs.
//
// One method, because a listing is one question and needs no transaction. The
// reader takes a Querier instead, which adds BeginSnapshot and Rollback: a
// model has to be of one moment, and eleven statements outside a snapshot
// produce a table that lost every column between the first and the second. A
// tree has no such requirement — it is redrawn — and paying for a guarantee
// nobody uses is paying twice.
type Asker interface {
	Query(ctx context.Context, sql string, args ...any) driver.Rows
}

// Filter narrows a listing of schemas at the server.
type Filter struct {
	// Pattern matches a name as a case-insensitive substring. Empty matches
	// everything, which is what strpos answers for an empty needle rather than
	// a case this code special-cases.
	Pattern string

	// System includes the schemas PostgreSQL keeps for itself: pg_catalog,
	// information_schema, and the pg_ ones the server creates for TOAST and
	// for temporary tables. They are out by default because nobody browsing
	// their own database is looking for them, and because a schema whose name
	// starts with pg_ cannot be anybody's — the server refuses to create one.
	System bool
}

// Lister answers what one level of the object tree holds.
//
// It is a separate type from Reader, over a narrower interface, because it
// answers a different question. Reader answers the schema: every column, every
// index, every constraint, normalised so that two readings of the same
// structure compare equal. That is what a diff needs and it is expensive for
// the right reason — a thousand tables in a fixed number of statements, against
// a budget of five seconds.
//
// A tree needs a name and a kind. Expanding a schema of five thousand tables
// has to answer in under a second, and the whole model of five thousand tables
// is orders of magnitude more data than five thousand lines of text can show.
// So this asks one question per level and carries back two columns.
//
// The reader is not replaced by it: the properties panel and the DDL tab read
// the schema whole, which is the work they are actually doing, and the cache
// pays for it once.
type Lister struct{ server Asker }

// NewLister builds a lister over a connection.
func NewLister(server Asker) *Lister { return &Lister{server: server} }

// listUserSchemas answers the schemas somebody made.
//
// The pg_ prefix is the whole system rule and not a list that goes stale:
// pg_catalog, pg_toast, pg_temp_1 and pg_toast_temp_1 all carry it, and the
// server refuses to create a schema that does. information_schema is named
// because it is the one that does not.
//
// strpos rather than LIKE, so that a name typed by a person is a substring and
// not a pattern: a person typing an underscore means an underscore, and LIKE
// would read it as "any character" — which matches more than what was asked
// for, silently. It also makes the empty pattern the ordinary case rather than
// a branch, because strpos of an empty needle is 1 in every row.
const listUserSchemas = `SELECT n.nspname
	FROM pg_catalog.pg_namespace n
	WHERE pg_catalog.strpos(pg_catalog.lower(n.nspname), pg_catalog.lower($1)) OPERATOR(pg_catalog.>) 0
	  AND NOT (n.nspname OPERATOR(pg_catalog.~~) 'pg\_%'
	           OR n.nspname OPERATOR(pg_catalog.=) 'information_schema')`

// listEverySchema is the same question without the exclusion.
//
// Two constants rather than one carrying a switch. A query with a boolean in it
// has two behaviours and one name, and neither of them is what the constant
// says; these two each say what they select, and each is read by the checks
// that read every query in this package.
const listEverySchema = `SELECT n.nspname
	FROM pg_catalog.pg_namespace n
	WHERE pg_catalog.strpos(pg_catalog.lower(n.nspname), pg_catalog.lower($1)) OPERATOR(pg_catalog.>) 0`

// Schemas answers the schemas of the database this connection is on.
//
// The database is the connection's own: PostgreSQL does not reach across
// databases, so browsing another one is another connection rather than another
// query.
// Which query is a branch over two calls rather than one call over a chosen
// query. The check that crosses the queries sent against the constants read
// refuses SQL that arrives in a variable — it cannot know what a variable held
// — and it is right to: a query nothing reads is a query nothing checks
// qualifies its names, which is how this package regressed into CVE-2018-1058
// twice.
func (l *Lister) Schemas(ctx context.Context, filter Filter) ([]Name, error) {
	if filter.System {
		return schemaNames(l.server.Query(ctx, listEverySchema, filter.Pattern))
	}

	return schemaNames(l.server.Query(ctx, listUserSchemas, filter.Pattern))
}

// schemaNames drains a listing of schema names, in the order this package
// sorts by rather than the one the server happened to answer in.
func schemaNames(rows driver.Rows) ([]Name, error) {
	defer rows.Close()

	var schemas []Name

	for rows.Next() {
		var name string

		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("reading a schema name: %w", err)
		}

		schemas = append(schemas, NewName(name))
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing the schemas: %w", err)
	}

	slices.SortFunc(schemas, func(a, b Name) int { return strings.Compare(a.String(), b.String()) })

	return schemas, nil
}

// listObjects answers the name and kind of everything in a schema that the
// model knows how to be about.
//
// The four relkinds are the ones the model has a type for: 'r' and 'p' are
// tables — a partitioned table is a table, and leaving it out would hide from
// the tree something the reader, the writer and the diff all handle — 'v' is a
// view and 'S' a sequence. Materialised views, foreign tables and indexes are
// left out because this version has no model for them, and a tree that lists an
// object whose properties and DDL it then cannot show is worse than one that
// does not list it yet.
//
// Indexes are not missing by oversight: they belong to the table that carries
// them and the model holds them there.
//
// The pattern is matched the same way schemas are, for the same reason.
const listObjects = `SELECT c.relname, c.relkind
	FROM pg_catalog.pg_class c
	JOIN pg_catalog.pg_namespace n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace
	WHERE n.nspname OPERATOR(pg_catalog.=) $1
	  AND c.relkind OPERATOR(pg_catalog.=) ANY (ARRAY['r', 'p', 'v', 'S'])
	  AND pg_catalog.strpos(pg_catalog.lower(c.relname), pg_catalog.lower($2)) OPERATOR(pg_catalog.>) 0`

// Objects answers what a schema holds, as names and kinds.
//
// The pattern narrows the answer at the server. It is not applied again here:
// two definitions of what matching means disagree the first time one of them
// learns about case, and the one that decides is the one that reduces what
// crosses the wire.
func (l *Lister) Objects(ctx context.Context, schema Name, pattern string) ([]Object, error) {
	if !schema.Valid() {
		return nil, fmt.Errorf("%w: %q cannot name a schema", ErrSchemaNotFound, schema)
	}

	rows := l.server.Query(ctx, listObjects, schema.String(), pattern)
	defer rows.Close()

	var objects []Object

	for rows.Next() {
		var name, relkind string

		if err := rows.Scan(&name, &relkind); err != nil {
			return nil, fmt.Errorf("reading an object of %s: %w", schema, err)
		}

		kind, known := kindOfRelation(relkind)
		if !known {
			return nil, fmt.Errorf("%w: %s.%s came back as relkind %q, which the listing did not ask for",
				ErrInconsistentCatalog, schema, name, relkind)
		}

		objects = append(objects, Object{Kind: kind, Name: NewName(name)})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing the objects of %s: %w", schema, err)
	}

	slices.SortFunc(objects, compareObjects)

	return objects, nil
}

// kindOfRelation turns a relkind into the kind the model uses, and reports
// whether it is one this version has.
func kindOfRelation(relkind string) (ObjectKind, bool) {
	switch relkind {
	case "r", "p":
		return ObjectTable, true
	case "v":
		return ObjectView, true
	case "S":
		return ObjectSequence, true
	}

	return "", false
}
