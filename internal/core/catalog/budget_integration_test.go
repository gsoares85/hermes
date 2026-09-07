//go:build integration

package catalog_test

import (
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/core/catalog"
	"github.com/gsoares85/hermes/internal/testsupport"
)

// The performance budget of introspection: a schema of a thousand tables read
// in under five seconds.
//
// It is a benchmark that fails rather than a number on a chart, because a
// budget nobody enforces is a wish. A regression here is not a graph that
// drifts — it is the object tree taking ten seconds to open a database, and by
// the time somebody notices, the change that caused it is twenty commits back.
//
// What makes the budget reachable is the shape of the reading rather than any
// tuning: a fixed number of queries for the whole schema whatever it holds.
// Asking per object would be a thousand round trips before anything was
// assembled, and no amount of care afterwards would win that back.
//
// This does not guard that decision, and it used to claim it did. A thousand
// round trips against a container on the same machine cost a fraction of a
// second and would pass here without anybody learning that the reader had
// started asking per object. What guards it is
// TestReadingCostsTheSameNumberOfQueriesWhateverTheSchemaHolds, which counts
// the questions instead of timing them. This is the time, and only the time.
//
// Measured on the oldest server in the matrix, which is the slowest and the one
// a user is least likely to be able to upgrade.
func BenchmarkReadingAThousandTables(b *testing.B) {
	const (
		tables = 1000
		budget = 5 * time.Second
	)

	instance := testsupport.SharedPostgres(b, testsupport.SupportedVersions[0])
	session := testsupport.Session(b, instance)
	schema := catalog.NewName(testsupport.LargeSchema(b, instance, tables))

	reader := catalog.NewReader(session)

	var (
		read  catalog.Schema
		reads int
		err   error
	)

	for b.Loop() {
		if read, err = reader.Read(b.Context(), schema); err != nil {
			b.Fatalf("reading %s: %v", schema, err)
		}

		reads++
	}

	// The fixture is checked after the timing rather than trusted, because a
	// budget met by reading an empty schema is the easiest way for this test to
	// go green while meaning nothing. It is what would happen if the fixture
	// silently stopped building, and it is exactly the failure a performance
	// gate is worst at noticing.
	assertLarge(b, read, tables)

	if each := b.Elapsed() / time.Duration(reads); each > budget {
		b.Fatalf("reading %d tables took %s, over the budget of %s", tables, each, budget)
	}
}

// assertLarge fails unless the schema really is the large one, with everything
// the reading was supposed to have assembled.
func assertLarge(b *testing.B, read catalog.Schema, tables int) {
	b.Helper()

	// Every table has a key, an index and an identity sequence; every tenth has
	// a view; and all but the first has a foreign key to the one before it,
	// which is the edge the graph is built from.
	for _, count := range []struct {
		what string
		got  int
		want int
	}{
		{"tables", len(read.Tables), tables},
		{"sequences", len(read.Sequences), tables},
		{"views", len(read.Views), tables / 10},
	} {
		if count.got != count.want {
			b.Fatalf("the schema read %d %s, want %d", count.got, count.what, count.want)
		}
	}

	if len(read.Dependencies) < tables-1 {
		b.Fatalf("the schema read %d dependencies, want at least the %d foreign keys",
			len(read.Dependencies), tables-1)
	}
}
