package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gsoares85/hermes/internal/core/catalog"
)

// counted is a source that says how often it was asked, and can be made to wait
// so that two callers are certainly inside it at once.
type counted struct {
	reads atomic.Int64
	fail  error

	// held, when set, blocks every read until it is closed. It is what turns
	// "two callers might overlap" into "two callers do overlap": without it the
	// first read finishes before the second starts on any machine that is not
	// busy, and the test would pass whether or not the cache shares a reading.
	held chan struct{}

	// arrived is closed by the first read that blocks, so a test knows a caller
	// is inside the source rather than still on its way there.
	arrived chan struct{}
	once    sync.Once
}

func (c *counted) Read(_ context.Context, schema catalog.Name) (catalog.Schema, error) {
	c.reads.Add(1)

	if c.held != nil {
		c.once.Do(func() { close(c.arrived) })
		<-c.held
	}

	if c.fail != nil {
		return catalog.Schema{}, c.fail
	}

	return catalog.Schema{
		Name:   schema,
		Tables: []catalog.Table{{Name: catalog.NewName("orders")}},
	}, nil
}

// The coupling that makes the cache useful, checked by the compiler: what fills
// a cache is a Reader, and the interface it is asked for is declared here for
// exactly that.
var _ catalog.Source = (*catalog.Reader)(nil)

func blocking() *counted {
	return &counted{held: make(chan struct{}), arrived: make(chan struct{})}
}

// mustRead reads and fails the test when it does not answer.
func mustRead(t *testing.T, cache *catalog.Cache, schema string) catalog.Schema {
	t.Helper()

	read, err := cache.Read(t.Context(), catalog.NewName(schema))
	if err != nil {
		t.Fatalf("Read(%s) = %v", schema, err)
	}

	return read
}

// The point of the cache, stated plainly.
func TestASchemaIsReadOnceAndAnsweredFromMemoryAfterwards(t *testing.T) {
	t.Parallel()

	source := &counted{}
	cache := catalog.NewCache(source)

	first := mustRead(t, cache, "sales")
	second := mustRead(t, cache, "sales")

	if source.reads.Load() != 1 {
		t.Errorf("the server was read %d times, want once", source.reads.Load())
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("two reads answered two schemas:\n %+v\n %+v", first, second)
	}
}

// Two callers that arrive together share one reading of the server.
//
// It is the case the object tree produces by itself — a node expanded twice
// while the first expansion is still in flight — and the difference between
// sharing and not is a second round trip to answer a question already being
// answered. The source blocks until both callers are known to be waiting, so
// this cannot pass by the first read simply finishing first.
func TestTwoCallersAtOnceCostOneReading(t *testing.T) {
	t.Parallel()

	source := blocking()
	cache := catalog.NewCache(source)

	var group sync.WaitGroup

	answers := make([]catalog.Schema, 2)

	for i := range answers {
		group.Add(1)

		go func() {
			defer group.Done()

			read, err := cache.Read(t.Context(), catalog.NewName("sales"))
			if err != nil {
				t.Errorf("Read() = %v", err)
			}
			answers[i] = read
		}()
	}

	<-source.arrived
	close(source.held)
	group.Wait()

	if source.reads.Load() != 1 {
		t.Errorf("the server was read %d times, want once for two callers", source.reads.Load())
	}
	if !reflect.DeepEqual(answers[0], answers[1]) {
		t.Errorf("the two callers were answered two schemas:\n %+v\n %+v", answers[0], answers[1])
	}
}

// Many readers at once, which is what -race is here to watch.
//
// Nothing about the cache is allowed to be a data race: the map is guarded, and
// what comes out is only ever read. A failure here is the detector's, not an
// assertion's, which is why the test asserts so little.
func TestManyReadersAtOnceIsNotARace(t *testing.T) {
	t.Parallel()

	cache := catalog.NewCache(&counted{})

	var group sync.WaitGroup

	for i := range 50 {
		group.Add(1)

		go func() {
			defer group.Done()

			// Two schemas, so that readers both share a reading and start
			// separate ones, and an invalidation lands in the middle of both.
			schema := fmt.Sprintf("schema_%d", i%2)

			read, err := cache.Read(t.Context(), catalog.NewName(schema))
			if err != nil {
				t.Errorf("Read() = %v", err)

				return
			}

			// Reading what came out, because that is what every caller does
			// with it and it is where a shared model would be caught racing.
			if len(read.Tables) != 1 {
				t.Errorf("the schema came back with %d tables", len(read.Tables))
			}

			if i%10 == 0 {
				cache.Invalidate(catalog.NewName(schema))
			}
		}()
	}

	group.Wait()
}

// Invalidating one schema sends the next read of it to the server.
func TestInvalidatingASchemaMakesTheNextReadGoToTheServer(t *testing.T) {
	t.Parallel()

	source := &counted{}
	cache := catalog.NewCache(source)

	mustRead(t, cache, "sales")
	cache.Invalidate(catalog.NewName("sales"))
	mustRead(t, cache, "sales")

	if source.reads.Load() != 2 {
		t.Errorf("the server was read %d times, want twice", source.reads.Load())
	}
}

// The key is the schema, so invalidating one leaves the others alone.
//
// Expanding a node in the object tree must not cost the reading of its
// neighbour, and running DDL against one schema is no reason to throw away
// another that somebody waited seconds for.
func TestInvalidatingOneSchemaLeavesTheOthers(t *testing.T) {
	t.Parallel()

	source := &counted{}
	cache := catalog.NewCache(source)

	mustRead(t, cache, "sales")
	mustRead(t, cache, "public")

	cache.Invalidate(catalog.NewName("sales"))

	mustRead(t, cache, "public")

	if source.reads.Load() != 2 {
		t.Errorf("the server was read %d times, want the two first readings only",
			source.reads.Load())
	}
}

// Refreshing a connection means having no reason to believe one schema over
// another, so it forgets all of them.
func TestInvalidatingEverythingForgetsEverySchema(t *testing.T) {
	t.Parallel()

	source := &counted{}
	cache := catalog.NewCache(source)

	mustRead(t, cache, "sales")
	mustRead(t, cache, "public")

	cache.InvalidateAll()

	mustRead(t, cache, "sales")
	mustRead(t, cache, "public")

	if source.reads.Load() != 4 {
		t.Errorf("the server was read %d times, want all four", source.reads.Load())
	}
}

// Invalidating a schema nobody read is not an error, because the caller has no
// way to know: whoever runs DDL knows what it changed, not what was cached.
func TestInvalidatingSomethingNeverReadIsHarmless(t *testing.T) {
	t.Parallel()

	source := &counted{}
	cache := catalog.NewCache(source)

	cache.Invalidate(catalog.NewName("never"))
	cache.InvalidateAll()

	mustRead(t, cache, "sales")

	if source.reads.Load() != 1 {
		t.Errorf("the server was read %d times, want once", source.reads.Load())
	}
}

// A failed reading is answered and then forgotten.
//
// A connection that dropped or a statement that timed out is not a fact about
// the schema. Keeping it would make a moment's trouble permanent, until
// somebody thought to invalidate a schema that never loaded in the first place.
func TestAFailedReadingIsNotKept(t *testing.T) {
	t.Parallel()

	failure := errors.New("the connection went away")
	source := &counted{fail: failure}
	cache := catalog.NewCache(source)

	if _, err := cache.Read(t.Context(), catalog.NewName("sales")); !errors.Is(err, failure) {
		t.Errorf("Read() = %v, want the failure the source reported", err)
	}

	source.fail = nil

	mustRead(t, cache, "sales")

	if source.reads.Load() != 2 {
		t.Errorf("the server was read %d times, want the failure to have been retried",
			source.reads.Load())
	}
}

// Everyone waiting on a reading that failed is told it failed.
func TestAFailureReachesEveryCallerWaitingOnIt(t *testing.T) {
	t.Parallel()

	failure := errors.New("the connection went away")
	source := blocking()
	source.fail = failure

	cache := catalog.NewCache(source)

	var group sync.WaitGroup

	for range 2 {
		group.Add(1)

		go func() {
			defer group.Done()

			if _, err := cache.Read(t.Context(), catalog.NewName("sales")); !errors.Is(err, failure) {
				t.Errorf("Read() = %v, want the failure", err)
			}
		}()
	}

	<-source.arrived
	close(source.held)
	group.Wait()
}

// A caller that gives up stops waiting, and does not take the reading with it.
func TestACallerThatGivesUpStopsWaiting(t *testing.T) {
	t.Parallel()

	source := blocking()
	cache := catalog.NewCache(source)

	// The first caller owns the reading and stays in it.
	started := make(chan struct{})

	go func() {
		close(started)

		if _, err := cache.Read(context.Background(), catalog.NewName("sales")); err != nil {
			t.Errorf("the first caller got %v", err)
		}
	}()

	<-started
	<-source.arrived

	giveUp, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := cache.Read(giveUp, catalog.NewName("sales")); !errors.Is(err, context.Canceled) {
		t.Errorf("the caller that gave up got %v, want the cancellation", err)
	}

	close(source.held)
}

// What the cache answers is shared, and Clone is how a caller gets one it may
// edit.
//
// The sharing is deliberate — copying a model of a thousand tables on every
// read would pay the allocation the cache exists to avoid — so the way out has
// to work: a clone that still shared a slice would let an editor reach into the
// cache, and the next reader would see the edit without anybody having written
// to the cache at all.
func TestACloneCanBeEditedWithoutTouchingWhatTheCacheHolds(t *testing.T) {
	t.Parallel()

	cache := catalog.NewCache(&counted{})

	held := mustRead(t, cache, "sales")
	clone := held.Clone()

	clone.Tables[0].Name = catalog.NewName("edited")
	clone.Tables = append(clone.Tables, catalog.Table{Name: catalog.NewName("added")})

	again := mustRead(t, cache, "sales")

	if got := again.Tables[0].Name.String(); got != "orders" {
		t.Errorf("the cached schema now reads %q; the clone shared its tables", got)
	}
	if len(again.Tables) != 1 {
		t.Errorf("the cached schema now has %d tables", len(again.Tables))
	}
}

// A clone is equal to what it was cloned from, or it is not a clone.
func TestACloneIsTheSameSchema(t *testing.T) {
	t.Parallel()

	original := crowded(3)

	if clone := original.Clone(); !reflect.DeepEqual(original, clone) {
		t.Errorf("a clone differs from its original:\n %+v\n %+v", original, clone)
	}
}

// Every slice in the model is cloned, not shared.
//
// One test per level, because the failure is silent: a slice that was shared
// looks right until somebody edits it, and then it changes a value nobody
// wrote to.
func TestNoSliceOfTheModelSurvivesACloneShared(t *testing.T) {
	t.Parallel()

	original := crowded(2)
	clone := original.Clone()

	clone.Tables[0].Columns[0].Name = catalog.NewName("edited")
	clone.Tables[0].Constraints[0].Columns[0] = catalog.NewName("edited")
	clone.Tables[0].Indexes[0].Columns[0] = catalog.NewName("edited")
	clone.Tables[0].Inherits[0] = catalog.NewName("edited")
	clone.Sequences[0].Name = catalog.NewName("edited")
	clone.Views[0].Name = catalog.NewName("edited")
	clone.Dependencies[0].Reason = "edited"

	for what, edited := range map[string]bool{
		"a column":     original.Tables[0].Columns[0].Name.String() == "edited",
		"a key":        original.Tables[0].Constraints[0].Columns[0].String() == "edited",
		"an index":     original.Tables[0].Indexes[0].Columns[0].String() == "edited",
		"a parent":     original.Tables[0].Inherits[0].String() == "edited",
		"a sequence":   original.Sequences[0].Name.String() == "edited",
		"a view":       original.Views[0].Name.String() == "edited",
		"a dependency": original.Dependencies[0].Reason == "edited",
	} {
		if edited {
			t.Errorf("editing %s of the clone changed the original", what)
		}
	}
}

// crowded builds a schema with something at every level, so that a clone has
// something to get wrong.
func crowded(tables int) catalog.Schema {
	schema := catalog.Schema{Name: catalog.NewName("sales")}

	for i := range tables {
		named := fmt.Sprintf("table_%03d", i)

		schema.Tables = append(schema.Tables, catalog.Table{
			Name:     catalog.NewName(named),
			Inherits: []catalog.Name{catalog.NewName("parent")},
			Columns: []catalog.Column{
				{Name: catalog.NewName("id"), Position: 1, Type: catalog.NewTypeName("integer"), NotNull: true},
				{Name: catalog.NewName("label"), Position: 2, Type: catalog.NewTypeName("text")},
			},
			Constraints: []catalog.Constraint{{
				Name: catalog.NewName(named + "_pkey"), Kind: catalog.ConstraintPrimaryKey,
				Columns: []catalog.Name{catalog.NewName("id")}, Definition: "PRIMARY KEY (id)",
			}},
			Indexes: []catalog.Index{{
				Name: catalog.NewName(named + "_label"), Columns: []catalog.Name{catalog.NewName("label")},
				Definition: "CREATE INDEX " + named + "_label ON " + named + " USING btree (label)",
			}},
		})

		schema.Sequences = append(schema.Sequences, catalog.Sequence{
			Name: catalog.NewName(named + "_id_seq"), Type: catalog.NewTypeName("integer"),
		})

		schema.Views = append(schema.Views, catalog.View{
			Name: catalog.NewName("view_" + named), Definition: "SELECT id FROM " + named,
		})

		schema.Dependencies = append(schema.Dependencies, catalog.Dependency{
			Object: catalog.Object{Kind: catalog.ObjectView, Name: catalog.NewName("view_" + named)},
			Needs:  catalog.Object{Kind: catalog.ObjectTable, Name: catalog.NewName(named)},
			Reason: catalog.ReasonQuery,
		})
	}

	return schema
}

// What a cached schema of a thousand tables costs in memory, recorded.
//
// ADR-0011 asks for this number rather than a limit, and the reason is the
// budget it sits under: the cache is per connection, so three connections to
// large schemas are three models alive at once, against a ceiling of 200MB at
// rest. The day the model grows a field that costs real memory, this is where
// it becomes a number somebody can see instead of a surprise on somebody's
// laptop.
//
// The ceiling is the budget's own share — 200MB across three connections — and
// not a regression detector. It will not notice the model doubling, and it is
// not meant to: the logged line is what makes growth visible, and this only
// fires when a single connection could sink the budget on its own. Not
// parallel, so that nothing else is allocating while the heap is measured.
func TestWhatACachedSchemaOfAThousandTablesCosts(t *testing.T) {
	const (
		tables  = 1000
		ceiling = 64 << 20
	)

	held := crowded(tables)

	cost := heldBytes(&held)

	t.Logf("a cached schema of %d tables holds %.1f MB", tables, float64(cost)/(1<<20))

	if cost > ceiling {
		t.Errorf("a schema of %d tables holds %d bytes, over the %d the budget allows"+
			" for three connections at once", tables, cost, ceiling)
	}
}

// heldBytes answers roughly what is on the heap because of the value, by
// measuring with it alive and again once it is not.
//
// Roughly is the word: a Go heap is not an accountant, and the number moves
// with the allocator and with the race detector. It is why the assertion above
// is an order of magnitude rather than a bound, and why what the test really
// leaves behind is the logged number.
func heldBytes(value *catalog.Schema) uint64 {
	alive := heapAfterGC()

	// Alive across the measurement, which is the whole point: without this the
	// compiler is free to consider it dead and collect what is being weighed.
	runtime.KeepAlive(*value)

	*value = catalog.Schema{}

	freed := heapAfterGC()

	// Subtracted as signed and floored at zero. The heap can read larger after
	// dropping the value than before — something else allocated, the allocator
	// kept a span — and an unsigned subtraction would answer that as sixteen
	// exabytes and fail the ceiling on a run where nothing was wrong.
	if freed >= alive {
		return 0
	}

	return alive - freed
}

func heapAfterGC() uint64 {
	// Twice, because the first collection can leave finalisable objects that
	// only the second one frees.
	runtime.GC()
	runtime.GC()

	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)

	return stats.HeapAlloc
}
