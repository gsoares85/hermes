package catalog

import (
	"context"
	"fmt"
)

// listSequences reads the sequences of a schema, with the parameters that
// decide what they hand out and the column that owns them.
//
// pg_sequence is where those parameters live from PostgreSQL 10 onwards, and
// the floor of the matrix is 12 — so there is one shape to read here and no
// branch on the version.
//
// The owner comes from pg_depend, and the two kinds of dependency it can carry
// are both taken. An AUTO dependency ('a') is a sequence a column owns: what
// serial creates and what OWNED BY declares. An INTERNAL dependency ('i') is
// the sequence the server made for an identity column, which is not an object
// anybody declared — and it is read anyway, which is the decision worth
// spelling out, because the index backing a constraint is left out for what
// looks like the same reason.
//
// The two cases are not the same. That index carries nothing the constraint
// does not already say, so dropping it loses nothing. This sequence carries its
// start, its step and its name, and the column says none of them: an identity
// declared START WITH 5 INCREMENT BY 3 keeps those numbers here and nowhere
// else, and a table renamed after the fact keeps a sequence named after what it
// used to be called. Leaving the row out would lose all of that silently, and
// the copy at the other end would count from one.
//
// What the model does not do is pretend it is separately creatable. The DDL
// writer emits no CREATE SEQUENCE for a sequence whose owning column is an
// identity column — the column's own clause creates it — and it can tell,
// because the owner is right there in the model.
//
// refobjsubid > 0 is what makes the reference a column rather than a table: a
// dependency on the relation as a whole carries zero there, and joining that to
// pg_attribute finds nothing and answers a sequence owned by a column with no
// name.
//
// Nothing filters the owner by schema, because nothing has to: PostgreSQL
// refuses to link a sequence to a table in another namespace, so the reference
// is always to a table this read has already listed.
const listSequences = `SELECT c.relname,
	       pg_catalog.format_type(s.seqtypid, NULL),
	       s.seqstart,
	       s.seqincrement,
	       s.seqmin,
	       s.seqmax,
	       s.seqcache,
	       s.seqcycle,
	       t.relname,
	       a.attname
	FROM pg_catalog.pg_sequence s
	JOIN pg_catalog.pg_class c ON c.oid OPERATOR(pg_catalog.=) s.seqrelid
	JOIN pg_catalog.pg_namespace n ON n.oid OPERATOR(pg_catalog.=) c.relnamespace
	LEFT JOIN pg_catalog.pg_depend d
	       ON d.classid OPERATOR(pg_catalog.=) 'pg_catalog.pg_class'::regclass
	      AND d.objid OPERATOR(pg_catalog.=) c.oid
	      AND d.refclassid OPERATOR(pg_catalog.=) 'pg_catalog.pg_class'::regclass
	      AND d.refobjsubid OPERATOR(pg_catalog.>) 0
	      AND d.deptype OPERATOR(pg_catalog.=) ANY (ARRAY['a', 'i'])
	LEFT JOIN pg_catalog.pg_class t ON t.oid OPERATOR(pg_catalog.=) d.refobjid
	LEFT JOIN pg_catalog.pg_attribute a
	       ON a.attrelid OPERATOR(pg_catalog.=) d.refobjid AND a.attnum OPERATOR(pg_catalog.=) d.refobjsubid
	WHERE n.nspname OPERATOR(pg_catalog.=) $1`

func (r *Reader) sequences(ctx context.Context, schema Name) ([]Sequence, error) {
	rows := r.server.Query(ctx, listSequences, schema.String())
	defer rows.Close()

	var sequences []Sequence

	for rows.Next() {
		var (
			name, typeName             string
			start, increment, min, max int64
			cache                      int64
			cycle                      bool
			table, column              *string
		)

		if err := rows.Scan(&name, &typeName, &start, &increment, &min, &max,
			&cache, &cycle, &table, &column); err != nil {
			return nil, fmt.Errorf("reading a sequence of %s: %w", schema, err)
		}

		sequences = append(sequences, Sequence{
			Name:      NewName(name),
			Type:      NewTypeName(typeName),
			Start:     start,
			Increment: increment,
			Min:       min,
			Max:       max,
			Cache:     cache,
			Cycle:     cycle,
			OwnedBy: ColumnRef{
				Table:  NewName(text(table)),
				Column: NewName(text(column)),
			},
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing the sequences of %s: %w", schema, err)
	}

	return sequences, nil
}
