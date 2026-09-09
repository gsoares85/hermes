//go:build integration

package catalog_test

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/testsupport"
)

// The server says what each query resolved to, and all of it is in pg_catalog.
//
// Reading the text of a query and refusing the shapes that resolve by name is a
// check whose author has to know every such shape, and three designs of it have
// now failed on a shape their author did not know: a bare operator, a bare
// function, and CASE with an expression before the first WHEN, which desugars
// to an = that appears nowhere in the text. Each was found by a person.
//
// This asks the other question. A view records, in pg_depend, every function,
// operator, relation, type, collation and operator class its query resolved to
// — recorded from the parse tree, so how the reference was spelled makes no
// difference. Building each query as a view under the corpus's own search path
// and reading those dependencies back is the server answering where it went,
// which is the thing the text was being read as a proxy for.
//
// It does not replace the text check, and the two are complementary rather than
// redundant. This one is blind to nothing in the syntax and blind to whatever
// the corpus does not shadow: a bare name with no shadow on the path resolves
// into pg_catalog and is reported clean, though the next schema on somebody's
// path could take it. The text check is the opposite — independent of what
// exists on the server, dependent on its author knowing the syntax. Neither
// alone is enough, which is why both are here.
func TestEveryQueryResolvesOnlyIntoTheCatalog(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			instance := testsupport.SharedPostgres(t, version)
			hostile := testsupport.Corpus(t, instance)
			views := hostile + "_oracle"

			instance.Exec(t, "CREATE SCHEMA "+views)

			queries := catalogQueries(t)
			named := map[string]query{}

			for at, name := range slices.Sorted(maps.Keys(queries)) {
				view := fmt.Sprintf("q%d", at)
				named[view] = queries[name]

				build(t, instance, hostile, views, view, queries[name].sql)
			}

			for _, resolved := range outside(t, instance, views) {
				view, what, _ := strings.Cut(resolved, "|")

				t.Errorf("%s resolves %s, which is not in the catalog; a schema on the"+
					" search path can declare one that beats it", named[view], what)
			}
		})
	}
}

// The oracle reports a query that resolves outside the catalog, checked by
// giving it one.
//
// Without this the oracle proves nothing: a query of its own that answered no
// rows for a reason of its own — a join that misses, a filter that is wrong,
// pg_identify_object answering a shape this version does not have — would pass
// every query in the package and read exactly like a clean result.
//
// The form given to it is the one the text check was blind to, because that is
// the pair this whole mechanism exists for.
func TestTheOracleReportsAQueryThatLeavesTheCatalog(t *testing.T) {
	t.Parallel()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])
	hostile := testsupport.Corpus(t, instance)
	views := hostile + "_canary"

	instance.Exec(t, "CREATE SCHEMA "+views)
	build(t, instance, hostile, views, "hijacked",
		`SELECT CASE d.classid WHEN 'pg_catalog.pg_class'::regclass THEN 1 ELSE 0 END
		 FROM pg_catalog.pg_depend d`)

	found := outside(t, instance, views)
	if len(found) != 1 || !strings.Contains(found[0], "operator "+hostile+".=") {
		t.Fatalf("the oracle reported %v for a query that resolves the corpus's own"+
			" shadowing operator, so it would report nothing for a real one", found)
	}
}

// The order of the search path decides which pg_class a bare name reads.
//
// Nothing in these queries qualifies a relation, a type or a collation, and
// that is safe only because pg_catalog is searched implicitly and implicitly
// first. Naming it explicitly — search_path TO schema, pg_catalog, which reads
// as the more careful of the two — puts the schema ahead of it and a bare
// pg_class becomes somebody else's table.
//
// The invariant lives in one constant in expr.go, guarded by a unit test that
// says the path is pointed at one schema. This is the measurement behind that
// test: without it the guard is a claim about PostgreSQL, and this branch has
// had to retract four of those.
func TestTheOrderOfTheSearchPathDecidesTheCatalog(t *testing.T) {
	t.Parallel()

	instance := testsupport.SharedPostgres(t, testsupport.SupportedVersions[0])
	hostile := testsupport.Corpus(t, instance)
	views := hostile + "_path"

	instance.Exec(t, "CREATE TABLE "+hostile+".pg_class (oid oid)")
	instance.Exec(t, "CREATE SCHEMA "+views)

	build(t, instance, hostile, views, "alone", "SELECT 1 FROM pg_class")

	if found := outside(t, instance, views); len(found) != 0 {
		t.Fatalf("a bare pg_class under a path of one schema resolved to %v,"+
			" so the reader's queries are not safe for the reason they claim", found)
	}

	instance.Exec(t, fmt.Sprintf(
		"SET search_path TO %s, pg_catalog; CREATE VIEW %s.after AS SELECT (EXISTS ("+
			"SELECT 1 FROM pg_class)) AS ok", hostile, views))

	found := outside(t, instance, views)
	if len(found) != 1 || !strings.Contains(found[0], "table "+hostile+".pg_class") {
		t.Errorf("naming pg_catalog after the schema resolved %v, want the schema's own"+
			" pg_class — which is the whole reason the path is pointed at one schema", found)
	}
}

// build creates one query as a view, under the search path the read uses.
//
// The query goes inside an EXISTS so that its columns need no names of their
// own: several of these answer a column twice, and CREATE VIEW refuses that for
// a reason that has nothing to do with what is being measured. The parse tree
// under the EXISTS is the whole query, so every dependency is recorded.
//
// The parameters become a text literal, which is the type the driver sends a Go
// string as — a bare literal would be untyped and would resolve a comparison
// against name rather than against text, which is a different operator from the
// one production asks for.
func build(t *testing.T, instance *testsupport.Instance, hostile, views, view, sql string) {
	t.Helper()

	arguments := strings.NewReplacer(
		"$1", "''::pg_catalog.text",
		"$2", "''::pg_catalog.text",
	)

	instance.Exec(t, fmt.Sprintf("SET search_path TO %s; CREATE VIEW %s.%s AS SELECT (EXISTS (%s)) AS ok",
		hostile, views, view, arguments.Replace(sql)))
}

// outside answers everything the views resolved to that does not live in
// pg_catalog, as "view|what".
//
// pg_identify_object is what makes this readable without a lookup table per
// catalog: it answers the kind and the qualified identity of anything pg_depend
// can point at, so a function, an operator, a relation, a type and a collation
// all come back the same way.
//
// The view's dependency on itself is excluded by oid rather than by kind, since
// it is the one reference that is not a resolution.
//
// It points the search path nowhere, because it does not need to: the identity
// pg_identify_object answers is qualified whatever the path is. A SET in front
// of it would also put psql's tag for that statement in the output, which is a
// line this would parse as a finding with no name.
const resolvedOutsideTheCatalog = `SELECT c.relname || '|' || o.type || ' ' || o.identity
	  FROM pg_catalog.pg_class c
	  JOIN pg_catalog.pg_rewrite r ON r.ev_class OPERATOR(pg_catalog.=) c.oid
	  JOIN pg_catalog.pg_depend d
	       ON d.classid OPERATOR(pg_catalog.=) 'pg_catalog.pg_rewrite'::pg_catalog.regclass
	      AND d.objid OPERATOR(pg_catalog.=) r.oid
	 CROSS JOIN LATERAL pg_catalog.pg_identify_object(d.refclassid, d.refobjid, 0) o
	 WHERE c.relnamespace OPERATOR(pg_catalog.=) '%s'::pg_catalog.regnamespace
	   AND d.refobjid OPERATOR(pg_catalog.<>) c.oid
	   AND o.schema IS DISTINCT FROM 'pg_catalog'
	 ORDER BY 1`

func outside(t *testing.T, instance *testsupport.Instance, views string) []string {
	t.Helper()

	var found []string

	for _, line := range strings.Split(instance.Exec(t,
		fmt.Sprintf(resolvedOutsideTheCatalog, views)), "\n") {
		trimmed := strings.TrimSpace(line)

		switch {
		case trimmed == "":
		case !strings.Contains(trimmed, "|"):
			// Said rather than skipped. Every row of this query carries the
			// separator, so a line without one is the oracle answering something
			// other than what it was asked — and the version of this that skipped
			// such a line reported every query in the package clean.
			t.Fatalf("the oracle answered %q, which is not a row it can produce", trimmed)
		default:
			found = append(found, trimmed)
		}
	}

	return found
}
