//go:build integration

package ddl_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
	"github.com/gsoares85/hermes/internal/core/ddl"
	"github.com/gsoares85/hermes/internal/driver"
	"github.com/gsoares85/hermes/internal/testsupport"
)

// The round trip: the acceptance criterion of the task, and the property the
// whole catalog was built to hold.
//
// A model is written to DDL, the DDL is applied to an empty schema, and that
// schema is read back. The two models have to be equal. A difference is a
// difference the structure diff would report between a database and an exact
// copy of it — the false positive that decides whether anybody trusts the
// product — and it is found here, on the pathological corpus and on every
// version the product supports, rather than by a user on a database nobody can
// share.
//
// The target is not empty of everything: it is given the types the corpus uses,
// because this version does not compare types and a model that does not hold
// one cannot write it. That is the boundary being tested, not a gap in the
// test — what the round trip proves is that everything the model does hold
// survives, and the script says out loud what it did not write.
func TestTheCorpusSurvivesARoundTrip(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			instance := testsupport.SharedPostgres(t, version)
			session := testsupport.Session(t, instance)

			original := readSchema(t, session, testsupport.Corpus(t, instance))
			script := ddl.Of(original)

			target := testsupport.CorpusTarget(t, instance)
			apply(t, instance, target, script)

			copied := readSchema(t, session, target)

			// A round trip over nothing passes. The corpus is large and the
			// comparison drops what the script declined, so this holds the
			// floor: the day a helper stops finding objects, or the writer
			// starts declining half the schema, this fails instead of going
			// quietly green.
			mustBeSubstantial(t, written(original, script))

			if difference := firstDifference(written(original, script), written(copied, script)); difference != "" {
				t.Errorf("PostgreSQL %s: the copy is not the original.\n%s\n\nThe script was:\n%s",
					version, difference, script)
			}
		})
	}
}

// What the corpus holds that the script declines, asserted rather than left to
// the round trip to work around silently.
//
// The round trip compares the two models with the declined objects taken out of
// both, so a writer that quietly stopped writing something would make the test
// easier to pass rather than harder. This is what stops that: the list of what
// was not written is exactly the partitioning, and anything else appearing in
// it fails here.
func TestTheScriptDeclinesOnlyWhatThisVersionDoesNotCompare(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			instance := testsupport.SharedPostgres(t, version)
			session := testsupport.Session(t, instance)

			script := ddl.Of(readSchema(t, session, testsupport.Corpus(t, instance)))

			var objects, keys []string

			for _, omission := range script.Omitted {
				if omission.Constraint.Valid() {
					keys = append(keys, omission.Constraint.String())

					continue
				}

				objects = append(objects, omission.Object.Name.String())
			}

			if want := []string{"measurements", "measurements_2026"}; !reflect.DeepEqual(objects, want) {
				t.Errorf("the script declined the objects %v, want %v", objects, want)
			}

			// One key and one only. The table it is on has another pointing at
			// an ordinary table, and that one has to be written — declining a
			// whole table because one of its keys cannot be added is what would
			// make a schema with a partitioned table in it write almost
			// nothing.
			want := []string{"reading_measurement_taken_fkey"}
			if !reflect.DeepEqual(keys, want) {
				t.Errorf("the script declined the keys %v, want %v", keys, want)
			}
		})
	}
}

func readSchema(t *testing.T, session driver.Session, schema string) catalog.Schema {
	t.Helper()

	read, err := catalog.NewReader(session).Read(t.Context(), catalog.NewName(schema))
	if err != nil {
		t.Fatalf("reading %s: %v", schema, err)
	}

	return read
}

// apply runs the script against a schema, with the session's search path
// naming it.
//
// The path is what makes the script portable: every identifier in it is bare,
// so the same statements build the same structure under whatever name the
// target has — which is what a copy between an environment and its double
// needs, and what the corpus proves by writing a schema called one thing into a
// schema called another.
func apply(t *testing.T, instance *testsupport.Instance, schema string, script ddl.Script) {
	t.Helper()

	for _, statement := range script.Statements {
		instance.Exec(t, `SET search_path TO "`+schema+`"; `+statement)
	}
}

// written is a model without the objects the script declined, and without the
// edges that touch one.
//
// Both sides go through it. The original holds objects the copy was never given
// and the copy holds none the original lacks, so taking the declined ones out
// of both is what leaves two models that are comparable at all — and doing it
// from the script's own list rather than by name is what keeps this honest: the
// test excludes what the writer said it excluded, and TestTheScriptDeclines...
// is what checks that list is the one it should be.
func written(schema catalog.Schema, script ddl.Script) catalog.Schema {
	declined := map[catalog.Object]bool{}
	declinedKeys := map[catalog.Name]bool{}

	for _, omission := range script.Omitted {
		if omission.Constraint.Valid() {
			declinedKeys[omission.Constraint] = true

			continue
		}

		declined[omission.Object] = true
	}

	kept := catalog.Schema{Name: schema.Name}

	for _, table := range schema.Tables {
		if declined[catalog.Object{Kind: catalog.ObjectTable, Name: table.Name}] {
			continue
		}

		kept.Tables = append(kept.Tables, withoutKeys(table, declinedKeys))
	}

	for _, sequence := range schema.Sequences {
		if !declined[catalog.Object{Kind: catalog.ObjectSequence, Name: sequence.Name}] {
			kept.Sequences = append(kept.Sequences, sequence)
		}
	}

	for _, view := range schema.Views {
		if !declined[catalog.Object{Kind: catalog.ObjectView, Name: view.Name}] {
			kept.Views = append(kept.Views, view)
		}
	}

	for _, edge := range schema.Dependencies {
		if !declined[edge.Object] && !declined[edge.Needs] {
			kept.Dependencies = append(kept.Dependencies, edge)
		}
	}

	return kept
}

// withoutKeys is a table without the constraints the script declined.
//
// A foreign key pointing at a table this version does not write is left out of
// the script, so the copy does not have it and the original does. Dropping it
// from both is what leaves two models that can be compared at all — and doing it
// from the script's own list keeps the test honest about which ones those are.
func withoutKeys(table catalog.Table, declined map[catalog.Name]bool) catalog.Table {
	if len(declined) == 0 {
		return table
	}

	kept := table
	kept.Constraints = nil

	for _, constraint := range table.Constraints {
		if !declined[constraint.Name] {
			kept.Constraints = append(kept.Constraints, constraint)
		}
	}

	return kept
}

// firstDifference answers where two models stop agreeing, so that a failure
// names the column rather than printing two schemas.
func firstDifference(want, got catalog.Schema) string {
	for _, difference := range []string{
		// The tables are checked for being the same tables first and compared
		// in detail second, so that one changed column reports the column
		// rather than printing two tables of fourteen.
		aligned("table", want.Tables, got.Tables, tableName),
		compareTables(want.Tables, got.Tables),
		compare("sequence", want.Sequences, got.Sequences, sequenceName),
		compare("view", want.Views, got.Views, viewName),
		compare("dependency", want.Dependencies, got.Dependencies, edgeName),
	} {
		if difference != "" {
			return difference
		}
	}

	return ""
}

// compareTables goes inside a table, and reports what is left of one only when
// everything inside it agrees.
func compareTables(want, got []catalog.Table) string {
	for i := range want {
		if i >= len(got) {
			break
		}

		named := want[i].Name.String()

		for _, difference := range []string{
			compare(named+" column", want[i].Columns, got[i].Columns, columnName),
			compare(named+" constraint", want[i].Constraints, got[i].Constraints, constraintName),
			compare(named+" index", want[i].Indexes, got[i].Indexes, indexName),
		} {
			if difference != "" {
				return difference
			}
		}

		if !reflect.DeepEqual(want[i], got[i]) {
			return fmt.Sprintf("the table %s differs in what it inherits or its partitioning."+
				"\n original: inherits %v, partitioned %v, partition %v"+
				"\n copy:     inherits %v, partitioned %v, partition %v",
				named,
				want[i].Inherits, want[i].Partitioned, want[i].Partition,
				got[i].Inherits, got[i].Partitioned, got[i].Partition)
		}
	}

	return ""
}

// aligned checks two lists hold the same objects in the same order, saying
// nothing about what is inside them.
func aligned[T any](what string, want, got []T, named func(T) string) string {
	for i := range want {
		switch {
		case i >= len(got):
			return fmt.Sprintf("the copy has no %s %s", what, named(want[i]))
		case named(want[i]) != named(got[i]):
			return fmt.Sprintf("%s %d is %s in the copy and %s in the original",
				what, i, named(got[i]), named(want[i]))
		}
	}

	if len(got) > len(want) {
		return fmt.Sprintf("the copy has an extra %s %s", what, named(got[len(want)]))
	}

	return ""
}

// compare is aligned, and then the first object whose fields differ.
func compare[T any](what string, want, got []T, named func(T) string) string {
	if difference := aligned(what, want, got, named); difference != "" {
		return difference
	}

	for i := range want {
		if !reflect.DeepEqual(want[i], got[i]) {
			return fmt.Sprintf("the %s %s differs.\n original: %+v\n copy:     %+v",
				what, named(want[i]), want[i], got[i])
		}
	}

	return ""
}

func tableName(t catalog.Table) string       { return t.Name.String() }
func sequenceName(s catalog.Sequence) string { return s.Name.String() }
func viewName(v catalog.View) string         { return v.Name.String() }
func columnName(c catalog.Column) string     { return c.Name.String() }
func indexName(i catalog.Index) string       { return i.Name.String() }

func constraintName(c catalog.Constraint) string { return c.Name.String() }

func edgeName(d catalog.Dependency) string {
	return d.Object.Name.String() + " needs " + d.Needs.Name.String() + " (" + string(d.Reason) + ")"
}

// mustBeSubstantial fails when there is too little left to compare for a pass
// to mean anything.
//
// The numbers are floors well under what the corpus holds, not counts of it: a
// test that had to be edited every time a fixture grew would be edited without
// being read. What they catch is the comparison collapsing to almost nothing,
// which is the shape a green run gets when it has stopped testing.
func mustBeSubstantial(t *testing.T, schema catalog.Schema) {
	t.Helper()

	columns := 0
	for _, table := range schema.Tables {
		columns += len(table.Columns)
	}

	if len(schema.Tables) < 10 || columns < 40 || len(schema.Sequences) < 4 ||
		len(schema.Views) < 4 || len(schema.Dependencies) < 6 {
		t.Fatalf("the comparison covers %d tables, %d columns, %d sequences, %d views and %d edges,"+
			" which is too little for a pass to mean anything",
			len(schema.Tables), columns, len(schema.Sequences),
			len(schema.Views), len(schema.Dependencies))
	}
}
