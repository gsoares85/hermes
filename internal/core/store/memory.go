package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// The double answers the contract, checked where it is written rather than
// only where it is tested: a method renamed on the interface should stop this
// file compiling, not a test file somewhere else.
var _ JobHistory = (*JobMemory)(nil)

// JobMemory is a job history that lives in the process and dies with it.
//
// It is the double every other part of the core is tested against: a use case
// that files a finished job can be proven without a file, a driver or a
// temporary directory. Unlike the in-memory vault, it is nobody's real store —
// a history that forgets on exit is no history at all — which is why it lives
// beside the contract rather than pretending to be a fallback.
//
// It is safe to use from several goroutines because the queue it will be fed
// by has a goroutine per job, and the panel reads while they write.
type JobMemory struct {
	mu      sync.RWMutex
	records map[string]JobRecord
}

// NewJobMemory creates an empty history.
func NewJobMemory() *JobMemory {
	return &JobMemory{records: make(map[string]JobRecord)}
}

// Save writes the job down, replacing whatever was under its identifier.
func (m *JobMemory) Save(ctx context.Context, record JobRecord) error {
	if err := Usable(ctx); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}

	// The caller's log is a window onto an array it goes on writing to. Kept
	// as it is, the history would be rewritten by whoever reused the buffer —
	// and the buffer this comes from is reused by design.
	record.Log = slices.Clone(record.Log)

	// To the millisecond and in UTC, which is what the contract promises and
	// what a store keeping a count of milliseconds since the epoch can offer.
	// A map could keep the nanosecond and the zone it was handed, and that is
	// exactly why it must not: a double that answers something no file ever
	// will proves every use case above it against a store nobody has.
	record.Started = kept(record.Started)
	record.Ended = kept(record.Ended)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.records[record.ID] = record

	return nil
}

// Get answers one job, or ErrNotFound.
func (m *JobMemory) Get(ctx context.Context, id string) (JobRecord, error) {
	if err := Usable(ctx); err != nil {
		return JobRecord{}, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	record, kept := m.records[id]
	if !kept {
		return JobRecord{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	record.Log = slices.Clone(record.Log)

	return record, nil
}

// Recent answers one page of the history, newest first.
func (m *JobMemory) Recent(ctx context.Context, page Page) ([]JobRecord, error) {
	if err := Usable(ctx); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	older := make([]JobRecord, 0, len(m.records))
	for _, record := range m.records {
		if page.After.Older(record) {
			older = append(older, record)
		}
	}

	// Newest first, and the identifier decides between two that ended
	// together — the same order the cursor walks, or a page would carry on
	// from a place the next one never reaches.
	slices.SortFunc(older, func(a, b JobRecord) int {
		if when := b.Ended.Compare(a.Ended); when != 0 {
			return when
		}

		return strings.Compare(b.ID, a.ID)
	})

	found := older[:min(page.Size(), len(older))]
	for i := range found {
		found[i].Log = slices.Clone(found[i].Log)
	}

	return found, nil
}

// kept is an instant as a history holds it, and the zero time left alone: a
// job that never started has no beginning, and truncating nothing would turn
// that into the epoch.
func kept(at time.Time) time.Time {
	if at.IsZero() {
		return at
	}

	return at.Truncate(time.Millisecond).UTC()
}

// Forget removes a job from the history.
func (m *JobMemory) Forget(ctx context.Context, id string) error {
	if err := Usable(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, kept := m.records[id]; !kept {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	delete(m.records, id)

	return nil
}
