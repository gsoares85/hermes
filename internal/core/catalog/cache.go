package catalog

import (
	"context"
	"fmt"
	"sync"
)

// Cache holds the schemas one connection has already read.
//
// One per open connection, in memory, dying with it, and shared with nothing —
// not even with another connection to the same database. That is the decision
// ADR-0011 records and the reason for it is not memory: what pg_catalog answers
// depends on the role that asked. Privileges decide which objects appear and
// RLS decides what a row shows, so two connections to one database under two
// roles have two legitimately different catalogs. A cache shared between them
// would show one person objects the other can see, or hide the ones they can,
// depending only on who arrived first.
//
// The key is the schema. Expanding one in the object tree must not cost the
// reading of its neighbour, and reading public must not invalidate sales.
//
// Invalidation is explicit and there is no expiry. A TTL is neither fresh nor
// predictable — thirty seconds is far too slow after an ALTER of your own and
// far too eager for a catalog nobody has touched — and the alternative that
// would be correct, listening for DDL events, means installing an event trigger
// in somebody else's database. So: whoever runs DDL through the tool invalidates
// what it changed, and whoever wants to see a change made from outside asks for
// it. What the screen shows is therefore always explainable.
//
// What comes out is shared and must not be modified. The model has no method
// that alters it, and a caller that needs one it can edit calls Schema.Clone.
// Go cannot enforce that — the fields are exported so they can be read — so it
// is a contract rather than a guarantee, and the reason it is worth having is
// on the other side: copying a model of a thousand tables on every read would
// pay the allocation the cache exists to avoid.
type Cache struct {
	source Source

	// mu guards the map and nothing else. It is never held across a read of the
	// server: a read takes seconds on a large schema, and a lock held over it
	// would make a second connection's cache wait for the first one's network.
	mu      sync.Mutex
	schemas map[Name]*pending
}

// Source is where a cache gets a schema it does not have.
//
// Declared here, in the package that consumes it, and satisfied by *Reader —
// which is what makes the cache testable with a double that counts how often it
// was asked, and therefore what makes "two readers, one read" a thing a test can
// state rather than a thing the code claims.
type Source interface {
	Read(ctx context.Context, schema Name) (Schema, error)
}

// pending is one reading of a schema: the answer, and a channel closed when
// there is one.
//
// It is in the map from the moment the reading starts rather than when it
// finishes, which is what makes a second caller for the same schema wait for
// the first instead of starting a reading of its own. Expanding a tree node
// twice in quick succession is exactly that, and two readings would cost two
// round trips to answer one question.
//
// Closing the channel is what publishes the fields: everything written before
// the close is visible to everything that reads after it.
type pending struct {
	done   chan struct{}
	schema Schema
	err    error
}

// NewCache builds a cache over whatever reads schemas.
func NewCache(source Source) *Cache {
	return &Cache{source: source, schemas: map[Name]*pending{}}
}

// Read answers the schema, from the cache when it is there and from the server
// when it is not.
//
// A caller that arrives while another is already reading the same schema waits
// for that reading rather than starting a second one. It waits on its own
// context as well, so a caller that gives up does not stay for an answer it no
// longer wants — but the reading itself belongs to whoever started it, and if
// that one's context is cancelled the reading fails for everyone waiting on it.
// They are told, and the failure is not kept, so asking again reads again.
func (c *Cache) Read(ctx context.Context, schema Name) (Schema, error) {
	reading, mine := c.reading(schema)
	if mine {
		c.fill(ctx, schema, reading)
	}

	select {
	case <-reading.done:
		return reading.schema, reading.err
	case <-ctx.Done():
		return Schema{}, fmt.Errorf("waiting for %s to be read: %w", schema, ctx.Err())
	}
}

// Invalidate forgets one schema, so that the next read of it goes to the
// server.
//
// One schema and not all of them: this is what the tool calls after running DDL
// it knows the target of, and invalidating the whole connection because one
// table changed would throw away every other schema somebody had waited for.
//
// A reading already under way is left to finish and to answer whoever is
// waiting on it. It is not in the map any more, so nothing later is served from
// it — which is the whole of what invalidating means.
func (c *Cache) Invalidate(schema Name) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.schemas, schema)
}

// InvalidateAll forgets everything, which is what somebody asking to refresh a
// connection means: they have no reason to believe one schema over another.
func (c *Cache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	clear(c.schemas)
}

// reading answers the reading of this schema, and whether this caller is the
// one that has to perform it.
func (c *Cache) reading(schema Name) (*pending, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if known, found := c.schemas[schema]; found {
		return known, false
	}

	started := &pending{done: make(chan struct{})}
	c.schemas[schema] = started

	return started, true
}

// fill performs the reading and publishes it.
//
// A failure is answered but not kept. A connection that dropped, a statement
// that timed out, a schema somebody was in the middle of creating: none of them
// is a fact about the schema worth remembering, and caching one would make a
// moment's trouble permanent until somebody thought to invalidate a schema that
// never loaded.
func (c *Cache) fill(ctx context.Context, schema Name, reading *pending) {
	reading.schema, reading.err = c.source.Read(ctx, schema)

	if reading.err != nil {
		c.forget(schema, reading)
	}

	close(reading.done)
}

// forget drops a reading, and only if it is still the one the map holds.
//
// The check matters: a failed reading that was invalidated and started again
// would otherwise delete the reading that replaced it, and the caller waiting
// on that one would be the only one ever served by it.
func (c *Cache) forget(schema Name, reading *pending) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.schemas[schema] == reading {
		delete(c.schemas, schema)
	}
}
