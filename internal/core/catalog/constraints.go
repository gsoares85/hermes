package catalog

import (
	"context"
	"fmt"
)

// listConstraints reads every constraint of every table in the schema.
//
// contype is filtered to the five kinds this model compares, and that filter is
// load-bearing rather than tidy. PostgreSQL 18 began cataloguing NOT NULL as a
// constraint of its own — contype 'n', one row per column — where every earlier
// version records it only as pg_attribute.attnotnull. Reading those rows would
// make the same schema produce two extra constraints per column on 18 and none
// on 17, which is a diff between two servers reporting changes nobody made.
// NOT NULL is modelled on the column, where every version agrees it lives.
//
// The key columns come back through unnest WITH ORDINALITY because the order of
// a key is part of it: (a, b) and (b, a) are different constraints, and an
// unnest without the ordinality is a join whose order the planner chooses.
//
// It is a lateral join rather than an ARRAY subquery so that the aggregation
// can name pg_catalog.array_agg and pg_catalog.unnest. Both must be qualified —
// the read points the search path at the schema being read, and a schema that
// declares unnest(smallint[]) would otherwise decide what a key is. See
// expr.go.
//
// pg_get_constraintdef carries what the columns cannot: the referenced table of
// a foreign key with its ON DELETE, the expression of a check, the operators of
// an exclusion. It is rendered from the parse tree, so two constraints written
// differently and meaning the same come back the same.
const listConstraints = `SELECT c.relname,
	       con.conname,
	       con.contype,
	       pg_catalog.pg_get_constraintdef(con.oid),
	       cols.columns
	FROM pg_catalog.pg_constraint con
	JOIN pg_catalog.pg_class c ON c.oid = con.conrelid
	JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
	LEFT JOIN LATERAL (
	        SELECT pg_catalog.array_agg(a.attname ORDER BY k.ord) AS columns
	        FROM pg_catalog.unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord)
	        JOIN pg_catalog.pg_attribute a
	          ON a.attrelid = con.conrelid AND a.attnum = k.attnum
	     ) cols ON true
	WHERE n.nspname = $1
	  AND c.relkind IN ('r', 'p')
	  AND con.contype IN ('p', 'f', 'u', 'c', 'x')`

func (r *Reader) constraints(ctx context.Context, schema Name, tables map[string]*Table) error {
	rows := r.server.Query(ctx, listConstraints, schema.String())
	defer rows.Close()

	for rows.Next() {
		var (
			table, name, kind, definition string
			columns                       []string
		)

		if err := rows.Scan(&table, &name, &kind, &definition, &columns); err != nil {
			return fmt.Errorf("reading a constraint of %s: %w", schema, err)
		}

		owner, known := tables[table]
		if !known {
			return fmt.Errorf("%w: %s.%s has the constraint %s and was not listed as a table",
				ErrInconsistentCatalog, schema, table, name)
		}

		owner.Constraints = append(owner.Constraints, Constraint{
			Name:       NewName(name),
			Kind:       constraintKind(kind),
			Columns:    namesOf(columns),
			Definition: definition,
		})
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("listing the constraints of %s: %w", schema, err)
	}

	return nil
}

// constraintKind spells out the single character the catalog stores.
//
// A kind this build does not know is carried through as it came rather than
// dropped or renamed to "unknown": ADR-0007 requires an object that is not
// compared to be named as not compared, and a kind that arrives here from a
// version newer than this one is exactly that case.
func constraintKind(stored string) ConstraintKind {
	switch stored {
	case "p":
		return ConstraintPrimaryKey
	case "f":
		return ConstraintForeignKey
	case "u":
		return ConstraintUnique
	case "c":
		return ConstraintCheck
	case "x":
		return ConstraintExclusion
	default:
		return ConstraintKind(stored)
	}
}

// namesOf turns the column list the catalog answered into names.
func namesOf(columns []string) []Name {
	if len(columns) == 0 {
		return nil
	}

	named := make([]Name, 0, len(columns))
	for _, column := range columns {
		named = append(named, NewName(column))
	}

	return named
}
