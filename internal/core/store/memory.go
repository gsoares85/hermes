package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
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

	m.mu.Lock()
	defer m.mu.Unlock()

	m.records[record.ID] = record

	return nil
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
