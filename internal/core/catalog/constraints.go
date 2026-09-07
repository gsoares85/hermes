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
// pg_get_constraintdef carries what the columns cannot: the ON DELETE of a
// foreign key, the expression of a check, the operators of an exclusion. It is
// rendered from the parse tree, so two constraints written differently and
// meaning the same come back the same.
//
// conparentid = 0 keeps out the constraints the server made rather than a
// person. Referencing a partitioned table creates one foreign key per partition
// on the referencing side, each pointing back at the declared one, and reading
// them put a second constraint in the model for a key somebody wrote once —
// a difference the diff would report against a schema that has none, and a
// duplicate ALTER TABLE in the script. The same field keeps out the copies a
// partition inherits from its parent.
//
// The referenced table comes back as structure as well as inside that text,
// because the writer has to act on it: a key pointing at a table this version
// declines to write cannot be written either. The join is restricted to the
// schema being read, the same boundary the dependency graph draws — a key
// pointing outside it is one the model has no way to name.
const listConstraints = `SELECT c.relname,
	       con.conname,
	       con.contype,
	       pg_catalog.pg_get_constraintdef(con.oid),
	       cols.columns,
	       ref.relname
	FROM pg_catalog.pg_constraint con
	JOIN pg_catalog.pg_class c ON c.oid OPERATOR(pg_catalog.=) con.conrelid
	JOIN pg_catalog.pg_namespace n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace
	LEFT JOIN pg_catalog.pg_class ref
	       ON ref.oid OPERATOR(pg_catalog.=) con.confrelid AND ref.relnamespace OPERATOR(pg_catalog.=) c.relnamespace
	LEFT JOIN LATERAL (
	        SELECT pg_catalog.array_agg(a.attname ORDER BY k.ord) AS columns
	        FROM pg_catalog.unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord)
	        JOIN pg_catalog.pg_attribute a
	          ON a.attrelid OPERATOR(pg_catalog.=) con.conrelid AND a.attnum OPERATOR(pg_catalog.=) k.attnum
	     ) cols ON true
	WHERE n.nspname OPERATOR(pg_catalog.=) $1
	  AND c.relkind OPERATOR(pg_catalog.=) ANY (ARRAY['r', 'p'])
	  AND con.contype OPERATOR(pg_catalog.=) ANY (ARRAY['p', 'f', 'u', 'c', 'x'])
	  AND con.conparentid OPERATOR(pg_catalog.=) 0`

func (r *Reader) constraints(ctx context.Context, schema Name, tables map[string]*Table) error {
	rows := r.server.Query(ctx, listConstraints, schema.String())
	defer rows.Close()

	for rows.Next() {
		var (
			table, name, kind, definition string
			columns                       []string
			references                    *string
		)

		if err := rows.Scan(&table, &name, &kind, &definition, &columns, &references); err != nil {
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
			References: NewName(text(references)),
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
