package catalog

import (
	"context"
	"fmt"
)

// The dependency graph is over objects of the model, and pg_depend is what
// fills it.
//
// pg_depend is not that graph. It relates catalog rows — a rewrite rule, a
// constraint, a default expression, a row of pg_class — and most of what it
// holds is about objects this model does not have. So every row is resolved to
// the object of the model that owns it before it becomes an edge, and every row
// that resolves to nothing the model holds is dropped rather than modelled.
//
// Four resolutions, which is the whole of it:
//
//   - a rewrite rule belongs to the view it rewrites, so a rule depending on a
//     relation is a view needing what it selects from;
//   - a foreign key belongs to the table it constrains, so a key depending on
//     the relation it points at is a table needing that table;
//   - a default expression belongs to the table its column is on, so a default
//     depending on a sequence is a table needing that sequence;
//   - a row of pg_class depending on another is inheritance, which is where a
//     child needs its parent and a partition needs the table it partitions.
//
// What is deliberately not an edge is the one that looks most like one. A
// sequence a column owns carries an AUTO dependency on that column, and reading
// it as "the sequence needs the table" would put every serial sequence after
// the table whose default calls it — a cycle in every schema that has one, and
// a false one, because OWNED BY is written as its own statement after both
// exist. The ownership is in the model already, on the sequence; what belongs
// in the graph is the default, which is a real ordering constraint. The
// refobjsubid test below is what separates them: a dependency on a whole
// relation is structural, a dependency on one of its columns is ownership.
//
// Edges that leave the schema are dropped for the same reason objects outside
// it are: the model is of one schema, and an order cannot place an object it
// does not hold. A view over a table in another schema is ordered as if it
// stood alone, which is correct here — the other schema is not this read's to
// create.

// listDependencies reads what every object of the schema needs to exist first.
//
// deptype is filtered to NORMAL and AUTO. INTERNAL is what ties an object to
// the thing that created it — the index behind a primary key, the sequence
// behind an identity column — and every one of those is already in the model as
// part of its owner, so an edge for it would order an object against something
// it is part of. The rest of the deptypes are extensions and pinned system
// objects, neither of which this model holds.
//
// The relkind filters on both ends are what keeps the graph over objects of the
// model and nothing else. They are the same filters the reads of tables, views
// and sequences use, so an edge can only name an object one of those queries
// already listed — and the reader checks that rather than trusting it.
//
// An edge from an object to itself is excluded here rather than tolerated
// later. A foreign key pointing at its own table and a view whose query refers
// to itself through a recursive CTE are both legal and both resolve where they
// stand; carried into the graph they would be cycles reported where there is
// nothing to order.
const listDependencies = `WITH edge AS (
	SELECT CASE d.classid
	         WHEN 'pg_catalog.pg_class'::regclass      THEN d.objid
	         WHEN 'pg_catalog.pg_rewrite'::regclass    THEN rw.ev_class
	         WHEN 'pg_catalog.pg_constraint'::regclass THEN con.conrelid
	         WHEN 'pg_catalog.pg_attrdef'::regclass    THEN ad.adrelid
	       END AS dependent,
	       CASE d.classid
	         WHEN 'pg_catalog.pg_class'::regclass      THEN 'inheritance'
	         WHEN 'pg_catalog.pg_rewrite'::regclass    THEN 'query'
	         WHEN 'pg_catalog.pg_constraint'::regclass THEN 'foreign key'
	         WHEN 'pg_catalog.pg_attrdef'::regclass    THEN 'default'
	       END AS reason,
	       d.refobjid AS needed
	FROM pg_catalog.pg_depend d
	LEFT JOIN pg_catalog.pg_rewrite rw
	       ON d.classid = 'pg_catalog.pg_rewrite'::regclass AND rw.oid = d.objid
	LEFT JOIN pg_catalog.pg_constraint con
	       ON d.classid = 'pg_catalog.pg_constraint'::regclass AND con.oid = d.objid
	      AND con.contype = 'f'
	LEFT JOIN pg_catalog.pg_attrdef ad
	       ON d.classid = 'pg_catalog.pg_attrdef'::regclass AND ad.oid = d.objid
	WHERE d.refclassid = 'pg_catalog.pg_class'::regclass
	  AND d.deptype IN ('n', 'a')
	  AND (d.classid <> 'pg_catalog.pg_class'::regclass OR d.refobjsubid = 0))
SELECT DISTINCT o.relkind, o.relname, e.reason, n.relkind, n.relname
	FROM edge e
	JOIN pg_catalog.pg_class o ON o.oid = e.dependent
	JOIN pg_catalog.pg_namespace os ON os.oid = o.relnamespace
	JOIN pg_catalog.pg_class n ON n.oid = e.needed
	JOIN pg_catalog.pg_namespace ns ON ns.oid = n.relnamespace
	WHERE os.nspname = $1
	  AND ns.nspname = $1
	  AND o.oid <> n.oid
	  AND o.relkind IN ('r', 'p', 'v', 'S')
	  AND n.relkind IN ('r', 'p', 'v', 'S')`

// dependencies answers the edges of the schema, checked against the objects it
// was read to hold.
func (r *Reader) dependencies(ctx context.Context, schema Name, known map[Object]bool) ([]Dependency, error) {
	rows := r.server.Query(ctx, listDependencies, schema.String())
	defer rows.Close()

	var found []Dependency

	for rows.Next() {
		var dependentKind, dependent, reason, neededKind, needed string

		if err := rows.Scan(&dependentKind, &dependent, &reason, &neededKind, &needed); err != nil {
			return nil, fmt.Errorf("reading a dependency of %s: %w", schema, err)
		}

		edge := Dependency{
			Object: Object{Kind: objectKind(dependentKind), Name: NewName(dependent)},
			Needs:  Object{Kind: objectKind(neededKind), Name: NewName(needed)},
			Reason: DependencyReason(reason),
		}

		if err := checkKnown(schema, known, edge); err != nil {
			return nil, err
		}

		found = append(found, edge)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing the dependencies of %s: %w", schema, err)
	}

	return found, nil
}

// checkKnown refuses an edge naming an object no other query listed.
//
// It should not happen: this query filters by the same schema and the same
// relkinds as the reads of tables, views and sequences. When it does, dropping
// the edge would be the worst of the options — the object it pointed at would
// keep its place in the order by luck rather than by constraint, and the DDL
// would fail on a target halfway through instead of here.
func checkKnown(schema Name, known map[Object]bool, edge Dependency) error {
	for _, object := range []Object{edge.Object, edge.Needs} {
		if !known[object] {
			// The unknown one leads, because it is what the reader is
			// complaining about; the edge follows as the context that makes the
			// complaint traceable. Naming the pair first read as though either
			// end might be the culprit.
			return fmt.Errorf("%w: the %s %s.%s was not listed, and %s depends on %s",
				ErrInconsistentCatalog, object.Kind, schema, object.Name,
				edge.Object.Name, edge.Needs.Name)
		}
	}

	return nil
}

// objectsOf is the set of objects a schema holds, which is what an edge may
// name.
func objectsOf(schema Schema) map[Object]bool {
	known := map[Object]bool{}

	for _, table := range schema.Tables {
		known[Object{Kind: ObjectTable, Name: table.Name}] = true
	}

	for _, sequence := range schema.Sequences {
		known[Object{Kind: ObjectSequence, Name: sequence.Name}] = true
	}

	for _, view := range schema.Views {
		known[Object{Kind: ObjectView, Name: view.Name}] = true
	}

	return known
}

// objectKind spells out the single character pg_class stores.
//
// A partitioned table and a plain one are both tables, and they have to arrive
// as the same kind: a partition and its parent are related by an edge, and two
// spellings of "table" would make that edge name an object the schema does not
// hold. A kind this build does not know is carried through as it came, for the
// reason ADR-0007 gives — an object that is not compared is named rather than
// dropped.
func objectKind(relkind string) ObjectKind {
	switch relkind {
	case "r", "p":
		return ObjectTable
	case "v":
		return ObjectView
	case "S":
		return ObjectSequence
	default:
		return ObjectKind(relkind)
	}
}
