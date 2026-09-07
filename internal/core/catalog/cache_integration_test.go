//go:build integration

package catalog_test

import (
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
	"github.com/gsoares85/hermes/internal/testsupport"
)

// The cache over a real server, and the promise it makes about what the screen
// shows: what you see is what was there when it was read, and it changes when
// you ask it to.
//
// It is the whole of ADR-0011's invalidation decision made visible. A change
// made outside the tool does not appear on its own — there is no expiry and
// nothing listens for DDL — and it does appear the moment somebody says so.
// Both halves matter: the first is what makes the screen explainable, the
// second is what keeps it from being wrong for ever.
func TestTheCacheAnswersFromMemoryUntilItIsToldNotTo(t *testing.T) {
	t.Parallel()

	session, instance := openSession(t, testsupport.SupportedVersions[0])
	corpus := testsupport.Corpus(t, instance)
	schema := catalog.NewName(corpus)

	cache := catalog.NewCache(catalog.NewReader(session))

	before := len(readThrough(t, cache, schema).Tables)

	instance.Exec(t, "CREATE TABLE "+corpus+".added_behind_the_cache (id integer)")

	if after := len(readThrough(t, cache, schema).Tables); after != before {
		t.Errorf("the schema read %d tables after the change and %d before;"+
			" the cache went back to the server", after, before)
	}

	cache.Invalidate(schema)

	if after := len(readThrough(t, cache, schema).Tables); after != before+1 {
		t.Errorf("the schema read %d tables after being invalidated, want %d", after, before+1)
	}
}

func readThrough(t *testing.T, cache *catalog.Cache, schema catalog.Name) catalog.Schema {
	t.Helper()

	read, err := cache.Read(t.Context(), schema)
	if err != nil {
		t.Fatalf("reading %s through the cache: %v", schema, err)
	}

	return read
}
