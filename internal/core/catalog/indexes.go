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
// constraint already created. The constraint owns it; conindid is what says so.
// A unique index somebody created directly is not a constraint and stays.
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
	       ARRAY(SELECT a.attname
	             FROM unnest(idx.indkey) WITH ORDINALITY AS k(attnum, ord)
	             JOIN pg_catalog.pg_attribute a
	               ON a.attrelid = idx.indrelid AND a.attnum = k.attnum
	             WHERE k.ord <= idx.indnkeyatts
	             ORDER BY k.ord)
	FROM pg_catalog.pg_index idx
	JOIN pg_catalog.pg_class i ON i.oid = idx.indexrelid
	JOIN pg_catalog.pg_class c ON c.oid = idx.indrelid
	JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
	WHERE n.nspname = $1
	  AND c.relkind IN ('r', 'p')
	  AND NOT EXISTS (
	        SELECT 1 FROM pg_catalog.pg_constraint con
	        WHERE con.conindid = idx.indexrelid)`

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
