package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gsoares85/hermes/internal/core/job"
)

// How much history one read answers.
//
// A screenful and a bit, rather than everything there is: a person who has
// been backing up nightly for a year has a history that no window shows at
// once and no process should hold in memory to draw the top of. The maximum is
// what stops a caller from asking for the whole thing by passing a large
// number instead of paging — the same defence the object tree already applies
// to a schema with ten thousand tables.
const (
	DefaultPageSize = 50
	MaxPageSize     = 500
)

// Errors of the job history. They are sentinels because the caller has
// something different to do about each: two are a record assembled wrongly,
// which is a bug above, and one is a row somebody dismissed twice, which is
// not.
var (
	// ErrNoID is a record that cannot be addressed. Without the identifier the
	// row can never be forgotten, corrected or matched to the job it came
	// from, and the history would grow a duplicate of it on every save.
	ErrNoID = errors.New("a history record needs the identifier of the job")

	// ErrNotOver is a job written down before it finished. The history is what
	// happened, and a row saying "running" outlives the only process that
	// could ever have corrected it.
	ErrNotOver = errors.New("a job that has not ended has no place in the history")

	// ErrNoEnd is a job that ended at no particular time. The end is what the
	// history is ordered by, so a record without one has no place in the list
	// rather than merely an empty column.
	ErrNoEnd = errors.New("a job in the history needs the time it ended")

	// ErrNotFound is a job the history does not have.
	ErrNotFound = errors.New("no such job in the history")
)

// JobRecord is a job that has ended, as the history keeps it.
//
// It is what the queue's View becomes once there is nothing left to watch: no
// progress, because a job that is over is at its end, and no elapsed time,
// because the two ends of it are here to subtract. The error is a string for
// the same reason it is one on the View — what anybody does with a failed job
// is read it or store it, never unwrap it.
type JobRecord struct {
	ID    string
	Kind  string
	Title string
	State job.State
	Err   string

	// Started is when the work began, and is the zero time for a job that was
	// cancelled before it ever started. That is a real outcome rather than a
	// missing value, and it is why the history is ordered by the end: every
	// job that reaches here has one of those.
	Started time.Time
	Ended   time.Time

	// Log is what the job said, already bounded by the ceiling the queue kept
	// it under and already stripped of secrets on the way in. Nothing here
	// redacts: what reaches the history is what was in the buffer, and the
	// buffer was clean.
	Log []string

	// Dropped is how many lines the ceiling took. A log stored without it is a
	// truncated log presented as the whole story, and whoever reads the first
	// line kept will read it as the beginning of the operation.
	Dropped int
}

// Validate reports whether the record can be written down.
//
// It lives beside the contract rather than inside one implementation, so that
// the double the core is tested against refuses exactly what the file on a
// user's machine refuses. A double that accepted more would prove the core
// against a store nobody has.
func (r JobRecord) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("%w: %s", ErrNoID, r.Kind)
	}

	if !r.State.Over() {
		return fmt.Errorf("%w: %s is %v", ErrNotOver, r.ID, r.State)
	}

	if r.Ended.IsZero() {
		return fmt.Errorf("%w: %s", ErrNoEnd, r.ID)
	}

	return nil
}

// Cursor answers where a page that ended on this record carries on from.
func (r JobRecord) Cursor() Cursor {
	return Cursor{Ended: r.Ended, ID: r.ID}
}

// Cursor is the place in the history one page stopped at.
//
// It carries the identifier as well as the time because two jobs can end in
// the same instant — a queue cancelled wholesale, or a clock coarser than the
// work — and a cursor that knew only the time would either repeat every job of
// that instant for ever or skip all but one of them.
//
// A keyset rather than an offset, for the reason the product applies to every
// list it draws: the history grows while somebody reads it, and an offset
// silently repeats one row and skips another when it does.
type Cursor struct {
	Ended time.Time
	ID    string
}

// IsZero reports whether the cursor names no place, which means the newest.
func (c Cursor) IsZero() bool {
	return c.Ended.IsZero() && c.ID == ""
}

// Older reports whether the record belongs on a page beginning at this cursor:
// it ended earlier, or in the same instant under a lower identifier.
//
// The definition lives here rather than in each implementation because it is
// the ordering itself, said once. An implementation that expresses it in SQL
// is saying this, and the conformance suite is what holds it to that.
func (c Cursor) Older(r JobRecord) bool {
	if c.IsZero() {
		return true
	}

	if r.Ended.Equal(c.Ended) {
		return r.ID < c.ID
	}

	return r.Ended.Before(c.Ended)
}

// Page asks for one screenful of history.
type Page struct {
	// Limit is how many records at most. Zero asks for DefaultPageSize.
	Limit int
	// After is where the previous page stopped. The zero Cursor starts at the
	// newest job.
	After Cursor
}

// Size answers how many records the page really asks for.
//
// Exported because every implementation has to resolve the limit the same way,
// and because a caller walking the pages needs to know when it has reached the
// end: a page shorter than the size it asked for is the last one.
func (p Page) Size() int {
	if p.Limit <= 0 {
		return DefaultPageSize
	}

	return min(p.Limit, MaxPageSize)
}

// JobHistory is what the application remembers about jobs that have ended.
//
// The contract is defined here, where it is consumed, and the only
// implementation that knows this is a database lives in internal/sqlitestore.
// See ADR-0014.
//
// Every method takes a context because a history is a file: on a machine whose
// disk is busy, or whose home directory is on a network share, reading one is
// not a map lookup. Implementations must honour cancellation even where their
// own store could answer instantly, or the core would be proven against a
// double easier to satisfy than the real one.
//
// Times are kept to the millisecond. That is what a store keeping a count of
// milliseconds since the epoch can honestly promise, and promising the
// nanosecond would be a contract only one implementation could keep.
type JobHistory interface {
	// Save writes the job down, replacing whatever was under its identifier.
	// Replacing rather than adding is what makes saving safe to retry, and
	// what stops one job from becoming two rows wearing the same identifier.
	Save(ctx context.Context, record JobRecord) error

	// Recent answers one page of the history, newest first. A page shorter
	// than the size it asked for is the last one.
	Recent(ctx context.Context, page Page) ([]JobRecord, error)

	// Forget removes a job from the history, answering ErrNotFound when there
	// was none. A row somebody dismissed twice is a normal outcome and the
	// caller ignores that error; an identifier computed wrongly is a bug, and
	// a silent success would hide it.
	Forget(ctx context.Context, id string) error
}

// Usable is the guard every history method begins with.
//
// Here rather than in each implementation for the same reason Validate is: a
// call that was given up on has to do nothing, whichever store is underneath.
func Usable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("giving up on the job history: %w", err)
	}

	return nil
}
