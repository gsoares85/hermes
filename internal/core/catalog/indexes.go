package catalog

import (
	"context"
	"fmt"
)

// listIndexes reads the indexes of a schema that stand on their own.
//
// An index backing a constraint is left out, and that is the decision this
// query encodes. PostgreSQL builds one for every primary key and every unique
// constraint, so listing both would put the same object in the model twice —
// and the DDL phase would emit a constraint and then an index that the
// constraint already created. The constraint owns it. A unique index somebody
// created directly is not a constraint and stays.
//
// conindid alone does not say ownership, and reading it that way deleted
// indexes nobody had made a constraint of. A foreign key also fills it in — with
// the index of the table it points at — so a plain CREATE UNIQUE INDEX that
// happens to be the target of somebody's foreign key was excluded as though the
// key owned it. It then went missing from the model, and the generated script
// died halfway through on the ALTER TABLE that adds the key, because the unique
// index it needs was never created.
//
// So ownership is three conditions, not one: the constraint's index is this
// index, the constraint is on the table this index is on — which is what the
// foreign key fails, since its conrelid is the referencing table — and the
// constraint is of a kind that owns an index at all.
//
// indnkeyatts is what separates the key from the payload of an INCLUDE, which
// is why the ordinality is filtered by it rather than the whole of indkey being
// taken: a column that is merely carried is not a column the index is ordered
// by, and a diff that confused the two would report a different index.
//
// An index by expression has a zero where a column number would be, so it joins
// to nothing and contributes no name. That is why the definition is kept: it is
// the only place the expression, the WHERE of a partial index and the payload
// of an INCLUDE survive.
//
// relispartition keeps out the copies the server makes rather than the indexes
// anybody declared. An index on a partitioned table is cloned onto every
// partition, so reading them puts one index in the model per partition for an
// index somebody wrote once — and the writer would emit each of them against a
// table whose own DDL already creates it.
//
// What that leaves unnamed is the primary key of a partition, which goes the
// same way: the constraint is filtered as a child and the index as a clone.
// Nothing is lost today, because a partition is not written at all — but this
// is the second half of "not compared has to be named", and when partitioning
// arrives it is these two lines that have to grow a reason rather than be
// discovered.
//
// indisvalid keeps out an index that does not work. A CREATE INDEX
// CONCURRENTLY that fails leaves one behind: it is in the catalog, it is not
// used by the planner, and it exists only to be dropped or rebuilt. Reading it
// would put a working index in the copy where the original has a broken one,
// which is not a copy — and the alternative, writing it as broken, is not
// something DDL can express. It is not compared, and this is where that is
// written down. pg_dump leaves them out for the same reason.
//
// The columns come back through a lateral join rather than an ARRAY subquery so
// that it can name pg_catalog.unnest and pg_catalog.array_agg. indkey is an
// int2vector, so the shadowing signature that would hijack it differs from the
// one that would hijack a constraint key — the corpus declares both. See
// expr.go.
//
// pg_get_indexdef is asked for the pretty form, and that is not about layout.
// It is the one renderer that ignores the search path: asked plainly it writes
// the table it indexes with the schema in front, whatever the path says,
// because the statement it produces is meant to stand on its own. The pretty
// flag is what makes it use the path like every other renderer — so the same
// index read under two schema names produces one definition instead of two, and
// the DDL writer supplies the schema it is writing to. The three-argument form
// is the only way to ask, and column 0 means the whole statement.
const listIndexes = `SELECT c.relname,
	       i.relname,
	       idx.indisunique,
	       idx.indisprimary,
	       pg_catalog.pg_get_indexdef(idx.indexrelid, 0, true),
	       cols.columns
	FROM pg_catalog.pg_index idx
	JOIN pg_catalog.pg_class i ON i.oid OPERATOR(pg_catalog.=) idx.indexrelid
	JOIN pg_catalog.pg_class c ON c.oid OPERATOR(pg_catalog.=) idx.indrelid
	JOIN pg_catalog.pg_namespace n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace
	LEFT JOIN LATERAL (
	        SELECT pg_catalog.array_agg(a.attname ORDER BY k.ord) AS columns
	        FROM pg_catalog.unnest(idx.indkey) WITH ORDINALITY AS k(attnum, ord)
	        JOIN pg_catalog.pg_attribute a
	          ON a.attrelid OPERATOR(pg_catalog.=) idx.indrelid AND a.attnum OPERATOR(pg_catalog.=) k.attnum
	        WHERE k.ord OPERATOR(pg_catalog.<=) idx.indnkeyatts
	     ) cols ON true
	WHERE n.nspname OPERATOR(pg_catalog.=) $1
	  AND c.relkind OPERATOR(pg_catalog.=) ANY (ARRAY['r', 'p'])
	  AND idx.indisvalid
	  AND i.relispartition OPERATOR(pg_catalog.=) false
	  AND NOT EXISTS (
	        SELECT 1 FROM pg_catalog.pg_constraint con
	        WHERE con.conindid OPERATOR(pg_catalog.=) idx.indexrelid
	          AND con.conrelid OPERATOR(pg_catalog.=) idx.indrelid
	          AND con.contype OPERATOR(pg_catalog.=) ANY (ARRAY['p', 'u', 'x']))`

func (r *Reader) indexes(ctx context.Context, schema Name, tables map[string]*Table) error {
	rows := r.server.Query(ctx, listIndexes, schema.String())
	defer rows.Close()

	for rows.Next() {
		var (
			table, name     string
			unique, primary bool
			definition      string
			columns         []string
		)

		if err := rows.Scan(&table, &name, &unique, &primary, &definition, &columns); err != nil {
			return fmt.Errorf("reading an index of %s: %w", schema, err)
		}

		owner, known := tables[table]
		if !known {
			return fmt.Errorf("%w: %s.%s has the index %s and was not listed as a table",
				ErrInconsistentCatalog, schema, table, name)
		}

		owner.Indexes = append(owner.Indexes, Index{
			Name:       NewName(name),
			Unique:     unique,
			Primary:    primary,
			Columns:    namesOf(columns),
			Definition: definition,
		})
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("listing the indexes of %s: %w", schema, err)
	}

	return nil
}
