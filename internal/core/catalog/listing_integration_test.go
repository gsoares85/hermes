//go:build integration

package catalog_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
	"github.com/gsoares85/hermes/internal/testsupport"
)

// listCorpus creates the pathological schema on a server of the given version
// and lists it.
func listCorpus(t *testing.T, version string) ([]catalog.Object, *catalog.Lister, string) {
	t.Helper()

	session, instance := openSession(t, version)
	schema := testsupport.Corpus(t, instance)
	lister := catalog.NewLister(session)

	objects, err := lister.Objects(t.Context(), catalog.NewName(schema), "")
	if err != nil {
		t.Fatalf("listing %s on PostgreSQL %s: %v", schema, version, err)
	}

	return objects, lister, schema
}

// objectNames answers the names of a listing, for a test that is about which objects
// came back rather than about what kind each one is.
func objectNames(objects []catalog.Object) []string {
	found := make([]string, 0, len(objects))
	for _, object := range objects {
		found = append(found, object.Name.String())
	}

	return found
}

// The listing runs against every server in the matrix and brings back the
// objects the model knows about.
//
// Against the corpus rather than a schema written for this test, because the
// corpus is where the names that break a tree live: one with a space, one with
// an accent, one that needs quoting for its capitals. A name that arrives
// wrong here arrives wrong in the tree, and this is the cheapest place to see
// it.
//
// The shapes asserted are the ones a tree would visibly lose: a partitioned
// table and its partition are both tables, inheritance shows both parent and
// child, and a view is a view.
func TestTheListingBringsBackWhatTheCorpusHolds(t *testing.T) {
	t.Parallel()

	for _, version := range testsupport.SupportedVersions {
		t.Run(version, func(t *testing.T) {
			t.Parallel()

			objects, _, schema := listCorpus(t, version)
			found := objectNames(objects)

			for _, want := range []string{
				"Customer",          // capitals, so it needs quoting
				"measurements",      // a partitioned table
				"measurements_2026", // a partition, which is a table too
				"parent",
				"child", // inheritance, both ends
				"circle_a",
				"circle_b",
			} {
				if !slices.Contains(found, want) {
					t.Errorf("PostgreSQL %s: %s is missing from the listing of %s", version, want, schema)
				}
			}

			kinds := map[catalog.ObjectKind]int{}
			for _, object := range objects {
				kinds[object.Kind]++
			}

			for _, kind := range []catalog.ObjectKind{catalog.ObjectTable, catalog.ObjectView, catalog.ObjectSequence} {
				if kinds[kind] == 0 {
					t.Errorf("PostgreSQL %s: the listing of %s holds no %s", version, schema, kind)
				}
			}
		})
	}
}

// A name that needs quoting comes back as the name, not as the quoting.
//
// The corpus holds one with a space and one with an accent. A tree that shows
// the quotes is showing something nobody typed, and a tree that loses the name
// cannot ask for the object again.
func TestTheListingKeepsANameThatNeedsQuoting(t *testing.T) {
	t.Parallel()

	objects, _, schema := listCorpus(t, testsupport.SupportedVersions[0])

	for _, name := range objectNames(objects) {
		if strings.Contains(name, `"`) {
			t.Errorf("%s.%s carries its quoting into the listing", schema, name)
		}
	}
}

// The pattern narrows the listing at the server, matching a substring in any
// case.
func TestThePatternNarrowsTheListingAtTheServer(t *testing.T) {
	t.Parallel()

	all, lister, schema := listCorpus(t, testsupport.SupportedVersions[0])

	narrowed, err := lister.Objects(t.Context(), catalog.NewName(schema), "CIRCLE")
	if err != nil {
		t.Fatalf("listing %s with a pattern: %v", schema, err)
	}

	if len(narrowed) == 0 || len(narrowed) >= len(all) {
		t.Fatalf("the pattern answered %d of %d objects, want some but not all",
			len(narrowed), len(all))
	}

	for _, name := range objectNames(narrowed) {
		if !strings.Contains(strings.ToLower(name), "circle") {
			t.Errorf("%s came back for the pattern CIRCLE", name)
		}
	}
}

// An underscore in a pattern is an underscore.
//
// It is what a person typing a name means, and it is what LIKE would not have
// done: there it stands for any character, so a pattern typed to narrow the
// tree would quietly widen it. The corpus holds circle_a and circle_b, so a
// pattern of "circle_" matches both and "circlea" matches neither.
func TestAnUnderscoreInAPatternIsAnUnderscore(t *testing.T) {
	t.Parallel()

	_, lister, schema := listCorpus(t, testsupport.SupportedVersions[0])

	matched, err := lister.Objects(t.Context(), catalog.NewName(schema), "circle_")
	if err != nil {
		t.Fatalf("listing %s: %v", schema, err)
	}

	if len(matched) < 2 {
		t.Errorf("the pattern circle_ answered %v, want both halves of the circle", objectNames(matched))
	}

	wild, err := lister.Objects(t.Context(), catalog.NewName(schema), "circlea")
	if err != nil {
		t.Fatalf("listing %s: %v", schema, err)
	}

	if len(wild) != 0 {
		t.Errorf("the pattern circlea answered %v, so the underscore was read as a wildcard", objectNames(wild))
	}
}

// The schemas PostgreSQL keeps for itself are out by default and in when
// asked, against a real catalog rather than against the text of the query.
func TestTheSystemSchemasAreOutOfTheListingByDefault(t *testing.T) {
	t.Parallel()

	session, instance := openSession(t, testsupport.SupportedVersions[0])
	corpus := testsupport.Corpus(t, instance)
	lister := catalog.NewLister(session)

	mine, err := lister.Schemas(t.Context(), catalog.Filter{})
	if err != nil {
		t.Fatalf("listing the schemas: %v", err)
	}

	listed := make([]string, 0, len(mine))
	for _, schema := range mine {
		listed = append(listed, schema.String())
	}

	if !slices.Contains(listed, corpus) {
		t.Errorf("the listing %v does not hold the corpus schema %s", listed, corpus)
	}

	for _, system := range []string{"pg_catalog", "pg_toast", "information_schema"} {
		if slices.Contains(listed, system) {
			t.Errorf("%s is in the default listing: %v", system, listed)
		}
	}

	every, err := lister.Schemas(t.Context(), catalog.Filter{System: true})
	if err != nil {
		t.Fatalf("listing every schema: %v", err)
	}

	all := make([]string, 0, len(every))
	for _, schema := range every {
		all = append(all, schema.String())
	}

	for _, system := range []string{"pg_catalog", "information_schema"} {
		if !slices.Contains(all, system) {
			t.Errorf("%s is missing from the listing that asked for everything: %v", system, all)
		}
	}
}

// A schema that is not there lists nothing, and that is not a failure.
//
// It is the difference the reader draws with ErrSchemaNotFound, and the tree
// does not need it: a node is expanded because it was listed, so a schema that
// vanished between the two is a stale tree rather than a fault. What would be
// a fault is answering rows.
func TestListingASchemaThatIsNotThereAnswersNothing(t *testing.T) {
	t.Parallel()

	session, _ := openSession(t, testsupport.SupportedVersions[0])

	objects, err := catalog.NewLister(session).Objects(t.Context(), catalog.NewName("no_such_schema"), "")
	if err != nil {
		t.Fatalf("listing a schema that is not there: %v", err)
	}

	if len(objects) != 0 {
		t.Errorf("it answered %v", objectNames(objects))
	}
}
